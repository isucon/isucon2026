package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNormalizeTarget(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"192.0.2.1", "http://192.0.2.1:80"},
		{"192.0.2.1:8080", "http://192.0.2.1:8080"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://example.test", "https://example.test"},
	} {
		got, err := normalizeTarget(tc.in, 80)
		if err != nil || got != tc.want {
			t.Errorf("%q: %q %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"", "ftp://example.test", "http://user:pass@example.test", "http://", "http://example.test?q=1"} {
		if _, err := normalizeTarget(bad, 80); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A subprocess is necessary: the real supervisor inherits an FD across exec.
func TestSupervisorChild(t *testing.T) {
	if os.Getenv("BENCH_TEST_CHILD") != "1" {
		return
	}
	code := run(context.Background(), strings.Split(os.Getenv("BENCH_TEST_ARGS"), "\n"), os.Stdout, os.Stderr)
	os.Exit(code)
}

func TestSupervisorIntegration(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "operator-only-detail", 500)
	}))
	defer server.Close()
	for _, post := range []bool{false, true} {
		t.Run(fmt.Sprint("post=", post), func(t *testing.T) {
			before := requests.Load()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			args := []string{"--target", server.URL, "--cycles", "1"}
			if post {
				args = append(args, "--only-post-validation")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorChild$")
			cmd.Env = append(os.Environ(), "BENCH_TEST_CHILD=1", "ISUXBENCH_REPORT_FD=3", "BENCH_TEST_ARGS="+strings.Join(args, "\n"))
			cmd.ExtraFiles = []*os.File{write}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			write.Close()
			var reports []*pb.BenchmarkResult
			for {
				var size uint16
				err := binary.Read(read, binary.BigEndian, &size)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data := make([]byte, int(size))
				if _, err := io.ReadFull(read, data); err != nil {
					t.Fatal(err)
				}
				p := new(pb.BenchmarkResult)
				if err := proto.Unmarshal(data, p); err != nil {
					t.Fatal(err)
				}
				reports = append(reports, p)
			}
			err = cmd.Wait()
			if err == nil || cmd.ProcessState.ExitCode() != 1 {
				t.Fatalf("exit: %v stderr=%s", err, &stderr)
			}
			if len(reports) == 0 {
				t.Fatal("no reports")
			}
			final := reports[len(reports)-1]
			if !final.Finished || final.Passed || final.Score != 0 || final.Execution == nil || final.MarkedAt == nil {
				t.Fatalf("final: %v", final)
			}
			if post {
				if requests.Load() != before {
					t.Fatal("post-validation touched target")
				}
			} else {
				if len(reports) < 2 || reports[0].Finished {
					t.Fatal("missing progress")
				}
				var public map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &public); err != nil {
					t.Fatal(err)
				}
				if len(public) != 2 || public["pass"] != false || public["score"] != float64(0) {
					t.Fatalf("public: %s", &stdout)
				}
				if !strings.Contains(stderr.String(), "expected 200, got 500") {
					t.Fatalf("missing private diagnostic: %s", &stderr)
				}
			}
			if strings.Contains(stdout.String(), "expected 200, got 500") || strings.Contains(final.Execution.Reason, "expected 200, got 500") {
				t.Fatal("private diagnostic leaked")
			}
		})
	}
}

func TestHostOverrideSurvivesSupervisorIntegration(t *testing.T) {
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		http.Error(w, "initialize unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--target", server.URL, "--host", "contest.example.test"}, &stdout, &stderr)
	if code != 1 || host != "contest.example.test" {
		t.Fatalf("exit=%d host=%q stderr=%s", code, host, &stderr)
	}
}
