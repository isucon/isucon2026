package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

var (
	ErrSanitizerUnavailable = errors.New("image sanitizer unavailable")
	ErrSanitizerMemoryLimit = errors.New("image sanitizer memory limit reached")
	ErrSanitizerCPULimit    = errors.New("image sanitizer CPU limit reached")
	ErrSanitizerCrash       = errors.New("image sanitizer crashed")
	ErrUnsupportedMedia     = errors.New("unsupported image media")
	ErrPayloadTooLarge      = errors.New("image payload too large")
)

type ResourceBudget struct {
	MemoryBytes uint64
	CPUSeconds  uint64
	Timeout     time.Duration
}

type Process interface {
	Run(context.Context, io.Reader, io.Writer, ResourceBudget) error
}

type Sanitizer interface {
	Sanitize(context.Context, io.Reader, io.Writer) error
}

type IsolatedSanitizer struct {
	process Process
	slots   chan struct{}
	budget  ResourceBudget
}

func NewIsolatedSanitizer(process Process, concurrency int, budget ResourceBudget) *IsolatedSanitizer {
	if concurrency < 1 {
		concurrency = 1
	}
	return &IsolatedSanitizer{process: process, slots: make(chan struct{}, concurrency), budget: budget}
}

func (s *IsolatedSanitizer) Sanitize(ctx context.Context, input io.Reader, output io.Writer) error {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ErrSanitizerUnavailable
	}
	runContext := ctx
	cancel := func() {}
	if s.budget.Timeout > 0 {
		runContext, cancel = context.WithTimeout(ctx, s.budget.Timeout)
	}
	defer cancel()
	err := s.process.Run(runContext, input, output, s.budget)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnsupportedMedia) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrSanitizerUnavailable, err)
}

type SubprocessProcess struct{ executable string }

func NewSubprocessProcess(executable string) *SubprocessProcess {
	return &SubprocessProcess{executable: executable}
}

func (p *SubprocessProcess) Run(ctx context.Context, input io.Reader, output io.Writer, budget ResourceBudget) error {
	directory, err := os.MkdirTemp("", "isureal-sanitizer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	inputPath, outputPath := filepath.Join(directory, "input.jpg"), filepath.Join(directory, "output.jpg")
	inputFile, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(inputFile, input); err != nil {
		inputFile.Close()
		return err
	}
	if err := inputFile.Close(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, p.executable, "--input", inputPath, "--output", outputPath)
	command.Env = []string{
		"LANG=C",
		"ISUREAL_SANITIZER_MEMORY_BYTES=" + strconv.FormatUint(budget.MemoryBytes, 10),
		"ISUREAL_SANITIZER_CPU_SECONDS=" + strconv.FormatUint(budget.CPUSeconds, 10),
	}
	err = command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return classifyProcessError(err)
	}
	result, err := os.Open(outputPath)
	if err != nil {
		return ErrSanitizerCrash
	}
	defer result.Close()
	_, err = io.Copy(output, result)
	return err
}

func classifyProcessError(err error) error {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return ErrSanitizerCrash
	}
	if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			switch status.Signal() {
			case syscall.SIGXCPU:
				return ErrSanitizerCPULimit
			case syscall.SIGKILL, syscall.SIGSEGV:
				return ErrSanitizerMemoryLimit
			}
		}
		if status.ExitStatus() == 65 {
			return ErrUnsupportedMedia
		}
	}
	return ErrSanitizerCrash
}
