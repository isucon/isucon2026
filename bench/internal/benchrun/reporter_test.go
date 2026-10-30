package benchrun

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"google.golang.org/protobuf/proto"
)

func TestReportFramesAndMonotonicTime(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(&buf)
	for _, finished := range []bool{false, true} {
		if err := r.Report(&pb.BenchmarkResult{Finished: finished, Passed: finished, Score: 29}); err != nil {
			t.Fatal(err)
		}
	}
	var last int64
	for i := range 2 {
		var length uint16
		if err := binary.Read(&buf, binary.BigEndian, &length); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(&buf, data); err != nil {
			t.Fatal(err)
		}
		var result pb.BenchmarkResult
		if err := proto.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		now := result.MarkedAt.AsTime().UnixNano()
		if now <= last || result.Finished != (i == 1) || result.Score != 29 {
			t.Fatalf("bad result: %v", &result)
		}
		last = now
	}
}

func TestFrameLimit(t *testing.T) {
	for _, size := range []int{65535, 65536} {
		var out bytes.Buffer
		err := writeFrame(&out, make([]byte, size))
		if size == 65535 && (err != nil || out.Len() != 65537) {
			t.Fatalf("boundary: %v", err)
		}
		if size == 65536 && (err == nil || out.Len() != 0) {
			t.Fatal("oversized frame written")
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }
func TestReportWriteError(t *testing.T) {
	if err := NewReporter(brokenWriter{}).Report(&pb.BenchmarkResult{}); err == nil {
		t.Fatal("lost write error")
	}
}
func TestOversizedLanguage(t *testing.T) {
	var b bytes.Buffer
	if err := NewReporter(&b).Report(&pb.BenchmarkResult{SurveyResponse: &pb.SurveyResponse{Language: strings.Repeat("a", 141)}}); err == nil {
		t.Fatal("accepted oversized language")
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return w.Buffer.Write(p)
}
func TestFrameShortWrites(t *testing.T) {
	var out shortWriter
	if err := writeFrame(&out, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), []byte{0, 3, 1, 2, 3}) {
		t.Fatalf("%v", out.Bytes())
	}
}

// TestTruncate は、Truncate が文字の境目で切り詰め、不正な UTF-8 を置き換えることを確かめる。
// 切り詰めた値で作った報告は上限に収まる。
func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"go", 140, "go"},
		{"abcdef", 3, "abc"},
		{"日本語", 7, "日本"},
		{"日本語", 2, ""},
		{"g\xffo", 140, "g�o"},
	} {
		if got := Truncate(tc.in, tc.n); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
	long := strings.Repeat("語", 100000)
	result := &pb.BenchmarkResult{
		SurveyResponse: &pb.SurveyResponse{
			Language: Truncate(long, MaxLanguageBytes), Agent: Truncate(long, MaxSurveyBytes),
			Harness: Truncate(long, MaxSurveyBytes), Model: Truncate(long, MaxSurveyBytes),
		},
		Execution: &pb.BenchmarkResult_Execution{Reason: Truncate(long, MaxReasonBytes)},
	}
	var b bytes.Buffer
	if err := NewReporter(&b).Report(result); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInheritedFD(t *testing.T) {
	for _, value := range []string{"", "not-a-number", "0", "1", "2", "-1", "999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ISUXBENCH_REPORT_FD", value)
			if _, _, err := FromEnv(); err == nil {
				t.Fatalf("accepted FD %q", value)
			}
		})
	}
}
