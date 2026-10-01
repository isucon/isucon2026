// Package benchrun implements the supervisor's length-prefixed protobuf pipe.
package benchrun

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	if len(result.GetSurveyResponse().GetLanguage()) > 140 {
		return fmt.Errorf("language exceeds 140 bytes")
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
	if len(data) > 65535 {
		return fmt.Errorf("result exceeds 65535 bytes")
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
