package testkit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

const advisoryLockID int64 = 20260719

func Database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("ISUREAL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ISUREAL_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockID)
		pool.Close()
	})
	Reset(t, pool)
	return pool
}

func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql")
	if err := schema.Reset(context.Background(), pool, schemaPath); err != nil {
		t.Fatal(err)
	}
}

func FixedClock() *clock.Fake {
	return clock.NewFake(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
}

type PushResult int

const (
	PushDelivered PushResult = iota
	PushTransientFailure
	PushUnknownResult
	PushPermanentFailure
)

type PushCall struct {
	NotificationID uuid.UUID
	Endpoint       string
}

type FakePush struct {
	mu      sync.Mutex
	Results []PushResult
	Calls   []PushCall
}

func (f *FakePush) Send(_ context.Context, notificationID uuid.UUID, endpoint string, _ []byte) PushResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, PushCall{NotificationID: notificationID, Endpoint: endpoint})
	if len(f.Results) == 0 {
		return PushDelivered
	}
	result := f.Results[0]
	f.Results = f.Results[1:]
	return result
}

type ControllableSanitizerProcess struct {
	Started chan struct{}
	Release chan struct{}
	Failure error
	Once    sync.Once
}

func (p *ControllableSanitizerProcess) Run(ctx context.Context, input io.Reader, output io.Writer, _ image.ResourceBudget) error {
	if p.Started != nil {
		p.Once.Do(func() { close(p.Started) })
		select {
		case <-p.Release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.Failure != nil {
		return p.Failure
	}
	_, err := io.Copy(output, input)
	return err
}

type HTTPHarness struct{ Echo *echo.Echo }

func NewHTTPHarness(configure func(*echo.Echo)) *HTTPHarness {
	e := echo.New()
	configure(e)
	return &HTTPHarness{Echo: e}
}

func (h *HTTPHarness) Do(method, target string, body []byte, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header = headers.Clone()
	recorder := httptest.NewRecorder()
	h.Echo.ServeHTTP(recorder, request)
	return recorder
}

func AssertOneActiveSession(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM login_sessions WHERE user_id=$1 AND active`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("active sessions = %d, want 1", count)
	}
}

func AssertNoImageFiles(t *testing.T, root string) {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Fatalf("unexpected image files: %v", found)
	}
}
