package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
)

func TestSanitizerSlotExhaustionCrashTimeoutAndRetry(t *testing.T) {
	blocking := &controllableProcess{started: make(chan struct{}), release: make(chan struct{})}
	sanitizer := isurealimage.NewIsolatedSanitizer(blocking, 1, isurealimage.ResourceBudget{MemoryBytes: 64 << 20, CPUSeconds: 1, Timeout: 50 * time.Millisecond})
	done := make(chan error, 1)
	go func() {
		done <- sanitizer.Sanitize(context.Background(), bytes.NewReader([]byte("input")), &bytes.Buffer{})
	}()
	<-blocking.started
	if err := sanitizer.Sanitize(context.Background(), bytes.NewReader([]byte("input")), &bytes.Buffer{}); !errors.Is(err, isurealimage.ErrSanitizerUnavailable) {
		t.Fatalf("slot exhaustion error = %v", err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatalf("first sanitizer: %v", err)
	}

	for _, failure := range []error{isurealimage.ErrSanitizerMemoryLimit, isurealimage.ErrSanitizerCPULimit, isurealimage.ErrSanitizerCrash, context.DeadlineExceeded} {
		process := &controllableProcess{failure: failure}
		sanitizer := isurealimage.NewIsolatedSanitizer(process, 1, isurealimage.ResourceBudget{MemoryBytes: 1, CPUSeconds: 1, Timeout: time.Millisecond})
		if err := sanitizer.Sanitize(context.Background(), bytes.NewReader([]byte("input")), &bytes.Buffer{}); !errors.Is(err, isurealimage.ErrSanitizerUnavailable) {
			t.Fatalf("failure %v mapped to %v", failure, err)
		}
		process.failure = nil
		if err := sanitizer.Sanitize(context.Background(), bytes.NewReader([]byte("input")), &bytes.Buffer{}); err != nil {
			t.Fatalf("retry after %v: %v", failure, err)
		}
	}
}

func TestSubprocessSanitizerCleansFilesAfterCrash(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "crash.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	process := isurealimage.NewSubprocessProcess(script)
	err := process.Run(context.Background(), bytes.NewReader([]byte("input")), &bytes.Buffer{}, isurealimage.ResourceBudget{MemoryBytes: 64 << 20, CPUSeconds: 1, Timeout: time.Second})
	if !errors.Is(err, isurealimage.ErrSanitizerCrash) {
		t.Fatalf("crash error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("subprocess temporary files remain: %v", entries)
	}
}

func TestSubprocessSanitizerDoesNotInheritApplicationSecrets(t *testing.T) {
	t.Setenv("ISUREAL_DATABASE_URL", "postgres://secret-database")
	t.Setenv("ISUREAL_VAPID_PRIVATE_KEY", "secret-vapid-private-key")
	directory := t.TempDir()
	script := filepath.Join(directory, "isolation.sh")
	body := `#!/bin/sh
if [ -n "$ISUREAL_DATABASE_URL" ] || [ -n "$ISUREAL_VAPID_PRIVATE_KEY" ]; then exit 9; fi
while [ "$#" -gt 0 ]; do
  case "$1" in --input) input="$2"; shift 2;; --output) output="$2"; shift 2;; *) shift;; esac
done
cp "$input" "$output"`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := isurealimage.NewSubprocessProcess(script).Run(context.Background(), strings.NewReader("isolated"), &output, isurealimage.ResourceBudget{MemoryBytes: 1, CPUSeconds: 1, Timeout: time.Second})
	if err != nil || output.String() != "isolated" {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}

type controllableProcess struct {
	started chan struct{}
	release chan struct{}
	failure error
}

func (p *controllableProcess) Run(ctx context.Context, input io.Reader, output io.Writer, budget isurealimage.ResourceBudget) error {
	if p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.failure != nil {
		return p.failure
	}
	_, err := io.Copy(output, input)
	return err
}
