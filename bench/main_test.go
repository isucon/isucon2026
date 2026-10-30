package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"google.golang.org/protobuf/encoding/protojson"
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

func TestReportReason(t *testing.T) {
	for _, tc := range []struct{ name, reason, want string }{
		{"success", "", "実行終了"},
		{"initialize", "初期化に失敗しました: POST /api/initialize: ステータスコード 500", "初期化に失敗しました"},
		{"multiple failures", "初期データの検証に失敗しました: ユーザーAの画像署名が不正\nユーザーBの画像署名が不正", "初期データの検証に失敗しました"},
		{"no detail", "初期化の応答の language が空です", "初期化の応答の language が空です"},
		{"interrupted", "ベンチマークが中断されました: context deadline exceeded", "ベンチマークが中断されました"},
		{"multiline without prefix", "検証に失敗しました\r\n追加の詳細", "検証に失敗しました"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportReason(tc.reason); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInvalidVerifyRate(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
	}{
		{"zero", "", []string{"-verify-rate", "0"}},
		{"above one", "", []string{"-verify-rate", "1.5"}},
		{"env out of range", "-1", nil},
		{"env not a number", "often", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ISUXBENCH_VERIFY_RATE", tc.env)
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), tc.args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit code %d, want 2: %s", code, stderr.String())
			}
		})
	}
}

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
	var initialized atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/api/initialize" && initialized.Load() {
			io.WriteString(w, `{"language":"go","agent":"custom-agent","harness":"custom-tool","model":"model-a,model-b"}`)
			return
		}
		http.Error(w, "operator-only-detail", 500)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		option   string
		wantExit int
		survey   bool
	}{
		{name: "contestant failure"},
		{name: "survey survives contestant failure", survey: true},
		{name: "unsupported post validation", option: "--only-post-validation", wantExit: 1},
		{name: "invalid configuration", option: "--users=0", wantExit: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initialized.Store(tc.survey)
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

				if reports[0].SurveyResponse != nil {
					t.Fatal("survey reported before initialization")
				}
				for _, report := range reports[1:] {
					if !tc.survey {
						if report.SurveyResponse != nil {
							t.Fatal("survey reported after failed initialization")
						}
						continue
					}
					data, err := protojson.Marshal(report.GetSurveyResponse())
					if err != nil {
						t.Fatal(err)
					}
					var survey map[string]any
					if err := json.Unmarshal(data, &survey); err != nil {
						t.Fatal(err)
					}
					if survey["language"] != "go" || survey["agent"] != "custom-agent" || survey["harness"] != "custom-tool" || survey["model"] != "model-a,model-b" {
						t.Fatalf("survey missing from report: %s", data)
					}
				}
				var public map[string]any
				if strings.Contains(stdout.String(), `msg="phase: initialize"`) {
					t.Fatalf("phase line leaked to public stdout: %s", &stdout)
				}
				if !strings.Contains(stderr.String(), `msg="phase: initialize"`) {
					t.Fatalf("phase line missing from operator stderr: %s", &stderr)
				}
				publicJSON := stdout.Bytes()
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
				wantReason := "初期化に失敗しました"
				if tc.survey {
					wantReason = "管理者としてログインできません"
				}
				if final.Execution.Reason != wantReason {
					t.Fatalf("reason: got %q, want %q", final.Execution.Reason, wantReason)
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

// readReports は supervisor への報告のフレームを、パイプが閉じるまで読む。
func readReports(t *testing.T, read io.Reader) []*pb.BenchmarkResult {
	t.Helper()
	var reports []*pb.BenchmarkResult
	for {
		var size uint16
		err := binary.Read(read, binary.BigEndian, &size)
		if err == io.EOF {
			return reports
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
}

// TestSurveyBoundsStillReportFailure は、初期化の応答の申告が supervisor への報告の上限を超えても、
// 報告に失敗せず最後の報告（Finished=true）まで送ることを確かめる。長すぎる language は初期化の
// fail、長すぎる agent、harness、model は切り詰めて報告する。
func TestSurveyBoundsStillReportFailure(t *testing.T) {
	for _, tc := range []struct {
		name, language, model string
		wantReason, wantModel string
		survey                bool
	}{
		{name: "control", language: "go", model: "normal", wantReason: "管理者としてログインできません", wantModel: "normal", survey: true},
		{name: "longest language", language: strings.Repeat("g", 140), model: "normal", wantReason: "管理者としてログインできません", wantModel: "normal", survey: true},
		{name: "long language", language: strings.Repeat("g", 141), model: "normal", wantReason: "初期化の応答の language が 140 バイトを超えています"},
		{name: "long model", language: "go", model: strings.Repeat("m", 70000), wantReason: "管理者としてログインできません", wantModel: strings.Repeat("m", 1024), survey: true},
		{name: "long multibyte model", language: "go", model: strings.Repeat("模", 1000), wantReason: "管理者としてログインできません", wantModel: strings.Repeat("模", 341), survey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/initialize" {
					_ = json.NewEncoder(w).Encode(map[string]string{"language": tc.language, "agent": "agent", "harness": "harness", "model": tc.model})
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			args := []string{"--target", server.URL, "--duration", "1s", "--timeout", "10s", "--evil=false"}
			cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorChild$")
			cmd.Env = append(os.Environ(), "BENCH_TEST_CHILD=1", "ISUXBENCH_REPORT_FD=3", "BENCH_TEST_ARGS="+strings.Join(args, "\n"))
			cmd.ExtraFiles = []*os.File{write}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				write.Close()
				t.Fatal(err)
			}
			write.Close()
			reports := readReports(t, read)
			_ = cmd.Wait()
			if code := cmd.ProcessState.ExitCode(); code != 0 || len(reports) == 0 {
				t.Fatalf("exit=%d frames=%d stderr=%s", code, len(reports), &stderr)
			}
			final := reports[len(reports)-1]
			if !final.Finished || final.Passed || final.GetExecution().GetReason() != tc.wantReason {
				t.Fatalf("final: %v", final)
			}
			for _, report := range reports {
				survey := report.GetSurveyResponse()
				if survey == nil {
					continue
				}
				if !tc.survey || survey.Language != tc.language || survey.Model != tc.wantModel {
					t.Fatalf("survey: language %d bytes, model %d bytes", len(survey.Language), len(survey.Model))
				}
			}
			if tc.survey && final.GetSurveyResponse() == nil {
				t.Fatal("survey missing from the final report")
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
