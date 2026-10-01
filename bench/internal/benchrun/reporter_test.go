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
	for i := 0; i < 2; i++ {
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
