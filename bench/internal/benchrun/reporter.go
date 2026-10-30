// Package benchrun implements the supervisor's length-prefixed protobuf pipe.
package benchrun

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 報告の項目の上限（バイト）。supervisor は language が MaxLanguageBytes を超える報告と、
// maxFrameBytes を超えるフレームを受け付けない。アプリが返した値は報告の前にこれらに収め、
// 最後の報告まで送れるようにする（各項目を上限まで使ってもフレームの上限より十分小さい）。
const (
	MaxLanguageBytes = 140
	// MaxSurveyBytes は agent、harness、model のそれぞれの上限。
	MaxSurveyBytes = 1024
	// MaxReasonBytes は Execution.Reason の上限。
	MaxReasonBytes = 4096
	maxFrameBytes  = 65535
)

// Truncate は s を有効な UTF-8 にし（不正なバイト列は U+FFFD にする）、n バイト以下になるよう
// 文字の境目で切り詰める。
func Truncate(s string, n int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type Reporter struct {
	mu     sync.Mutex
	writer io.Writer
	last   time.Time
}

func NewReporter(w io.Writer) *Reporter { return &Reporter{writer: w} }

// FromEnv takes ownership of the inherited pipe. An absent FD means standalone mode.
func FromEnv() (*Reporter, io.Closer, error) {
	value, present := os.LookupEnv("ISUXBENCH_REPORT_FD")
	if !present {
		return nil, nil, nil
	}
	// Descendants must not inherit the reporting channel and prevent EOF.
	os.Unsetenv("ISUXBENCH_REPORT_FD")
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil, nil, fmt.Errorf("invalid ISUXBENCH_REPORT_FD")
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return nil, nil, fmt.Errorf("report FD: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return nil, nil, fmt.Errorf("report FD must be a pipe")
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), "supervisor-report")
	return NewReporter(f), f, nil
}

func (r *Reporter) Report(result *pb.BenchmarkResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if result == nil {
		return fmt.Errorf("nil result")
	}
	if len(result.GetSurveyResponse().GetLanguage()) > MaxLanguageBytes {
		return fmt.Errorf("language exceeds %d bytes", MaxLanguageBytes)
	}
	result = proto.Clone(result).(*pb.BenchmarkResult)
	now := time.Now()
	if !now.After(r.last) {
		now = r.last.Add(time.Nanosecond)
	}
	result.MarkedAt = timestamppb.New(now)
	data, err := proto.Marshal(result)
	if err != nil {
		return err
	}
	if f, ok := r.writer.(*os.File); ok {
		if err := f.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
	}
	if err := writeFrame(r.writer, data); err != nil {
		return err
	}
	r.last = now
	return nil
}

func writeFrame(w io.Writer, data []byte) error {
	if len(data) > maxFrameBytes {
		return fmt.Errorf("result exceeds %d bytes", maxFrameBytes)
	}
	frame := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(frame, uint16(len(data)))
	copy(frame[2:], data)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
