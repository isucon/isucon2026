package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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
	for _, tc := range []struct {
		name     string
		option   string
		wantExit int
	}{
		{name: "contestant failure"},
		{name: "unsupported post validation", option: "--only-post-validation", wantExit: 1},
		{name: "invalid configuration", option: "--users=0", wantExit: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			args := []string{"--target", server.URL, "--duration", "1s"}
			if tc.option != "" {
				args = append(args, tc.option)
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
			if cmd.ProcessState.ExitCode() != tc.wantExit {
				t.Fatalf("exit: %v stderr=%s", err, &stderr)
			}
			if len(reports) == 0 {
				t.Fatal("no reports")
			}
			final := reports[len(reports)-1]
			if !final.Finished || final.Passed || final.Score != 0 || final.Execution == nil || final.MarkedAt == nil {
				t.Fatalf("final: %v", final)
			}
			if tc.option != "" {
				if requests.Load() != before {
					t.Fatal("configuration failure touched target")
				}
			} else {
				if len(reports) < 2 || reports[0].Finished {
					t.Fatal("missing progress")
				}
				var public map[string]any
				lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
				if len(lines) < 2 || !strings.Contains(lines[0], `msg="phase: initialize"`) {
					t.Fatalf("missing public progress: %s", &stdout)
				}
				if strings.Contains(stderr.String(), `msg="phase: initialize"`) {
					t.Fatalf("progress sent to stderr: %s", &stderr)
				}
				resultStart := strings.Index(stdout.String(), "\n{")
				if resultStart < 0 {
					t.Fatalf("missing public result: %s", &stdout)
				}
				publicJSON := stdout.Bytes()[resultStart+1:]
				if err := json.Unmarshal(publicJSON, &public); err != nil {
					t.Fatal(err)
				}
				var indented bytes.Buffer
				if err := json.Indent(&indented, bytes.TrimSpace(publicJSON), "", "  "); err != nil {
					t.Fatal(err)
				}
				indented.WriteByte('\n')
				if !bytes.Equal(publicJSON, indented.Bytes()) {
					t.Fatalf("public output formatting: got %q, want %q", publicJSON, indented.Bytes())
				}
				if len(public) != 3 || public["pass"] != false || public["score"] != float64(0) {
					t.Fatalf("public: %s", &stdout)
				}
				diagnostic := "ステータスコード 500 が返りました（期待値 200"
				if final.Execution.Reason != "実行終了" {
					t.Fatalf("reason: got %q, want %q", final.Execution.Reason, "実行終了")
				}
				messages, ok := public["messages"].([]any)
				if !ok || len(messages) == 0 {
					t.Fatalf("missing contestant messages: %s", &stdout)
				}
				message, ok := messages[0].(string)
				if !ok || !strings.Contains(message, diagnostic) {
					t.Fatalf("missing contestant messages: %s", &stdout)
				}
				if !strings.Contains(stderr.String(), diagnostic) {
					t.Fatalf("missing private diagnostic: %s", &stderr)
				}
			}
			if strings.Contains(stdout.String(), "operator-only-detail") || strings.Contains(final.Execution.Reason, "operator-only-detail") {
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
