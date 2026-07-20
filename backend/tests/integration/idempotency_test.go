package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/labstack/echo/v5"
	"gopkg.in/yaml.v3"
)

func TestMissingIdempotencyKeyReturnsBadRequestWithoutCallingHandler(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequest(http.MethodPost, "/state", strings.NewReader(`{"value":1}`))
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	called := false
	handler := httpapi.RequireIdempotency(nil, "state", func(*echo.Context, []byte) (idempotency.Scope, error) {
		return idempotency.Scope{Type: "user", Key: "test"}, nil
	})(func(*echo.Context) error { called = true; return nil })
	err := handler(ctx)
	var appError *httpapi.AppError
	if !errors.As(err, &appError) || appError.Status != http.StatusBadRequest || called {
		t.Fatalf("error=%v called=%v", err, called)
	}
}

func TestEveryStateChangingOpenAPIOperationRequiresIdempotencyKey(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	contract := filepath.Join(filepath.Dir(filename), "..", "..", "..", "specs", "001-isureal-social-app", "contracts", "openapi.yaml")
	body, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	for path, rawPath := range paths {
		operations := rawPath.(map[string]any)
		for method, rawOperation := range operations {
			operation := rawOperation.(map[string]any)
			parameters, _ := operation["parameters"].([]any)
			hasKey := false
			for _, rawParameter := range parameters {
				parameter := rawParameter.(map[string]any)
				if parameter["$ref"] == "#/components/parameters/IdempotencyKey" {
					hasKey = true
				}
			}
			switch method {
			case "post", "put", "patch", "delete":
				if !hasKey {
					t.Errorf("%s %s does not require Idempotency-Key", method, path)
				}
				responses := operation["responses"].(map[string]any)
				if _, ok := responses["409"]; !ok {
					t.Errorf("%s %s does not declare 409", method, path)
				}
			case "get":
				if hasKey {
					t.Errorf("GET %s must not accept Idempotency-Key", path)
				}
			}
		}
	}
}

func TestIdempotencyReplayConflictRetryExpiryAndCallerScopes(t *testing.T) {
	ctx := context.Background()
	pool := openTestPool(t, ctx)
	lockTestDatabase(t, ctx, pool)
	resetFoundation(t, ctx, pool)
	if _, err := pool.Exec(ctx, `CREATE TABLE idempotency_probe (value text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	fakeClock := clock.NewFake(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	repository := postgres.NewIdempotencyRepository(&postgres.DB{Pool: pool}, domain.NewDeterministicIDGenerator(testNamespace), fakeClock)
	key := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	request := idempotency.Request{Scope: idempotency.Scope{Type: "user", Key: "user-1"}, Operation: "update_profile", Key: key, RequestHash: "hash-a"}
	var executions atomic.Int32
	callback := func(ctx context.Context) (idempotency.Result, error) {
		executions.Add(1)
		if _, err := postgres.Executor(ctx, pool).Exec(ctx, `INSERT INTO idempotency_probe(value) VALUES('one')`); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: 200, Header: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)}, nil
	}
	first, replayed, err := repository.Execute(ctx, request, callback)
	if err != nil || replayed || first.Status != 200 {
		t.Fatalf("first = %#v, replayed=%v, err=%v", first, replayed, err)
	}
	second, replayed, err := repository.Execute(ctx, request, callback)
	if err != nil || !replayed || string(second.Body) != string(first.Body) || executions.Load() != 1 {
		t.Fatalf("replay = %#v, replayed=%v, executions=%d, err=%v", second, replayed, executions.Load(), err)
	}

	different := request
	different.RequestHash = "hash-b"
	if _, _, err := repository.Execute(ctx, different, callback); !errors.Is(err, idempotency.ErrRequestConflict) {
		t.Fatalf("different request error = %v", err)
	}

	otherScope := request
	otherScope.Scope.Key = "user-2"
	if _, _, err := repository.Execute(ctx, otherScope, func(context.Context) (idempotency.Result, error) { return idempotency.Result{Status: 204}, nil }); err != nil {
		t.Fatalf("authenticated caller scoping: %v", err)
	}
	registration := request
	registration.Scope = idempotency.Scope{Type: "registration", Key: "alice"}
	registration.Operation = "register"
	if _, _, err := repository.Execute(ctx, registration, func(context.Context) (idempotency.Result, error) {
		return idempotency.Result{Status: 201, Body: []byte(`{}`)}, nil
	}); err != nil {
		t.Fatalf("unauthenticated caller scoping: %v", err)
	}

	retryKey := request
	retryKey.Key = uuid.MustParse("10000000-0000-0000-0000-000000000002")
	result, replayed, err := repository.Execute(ctx, retryKey, func(ctx context.Context) (idempotency.Result, error) {
		_, err := postgres.Executor(ctx, pool).Exec(ctx, `INSERT INTO idempotency_probe(value) VALUES('rolled-back')`)
		return idempotency.Result{Status: 503, Body: []byte(`{"retry":true}`)}, err
	})
	if err != nil || replayed || result.Status != 503 {
		t.Fatalf("503 result=%#v replayed=%v err=%v", result, replayed, err)
	}
	var rolledBack int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_probe WHERE value='rolled-back'`).Scan(&rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("503 mutation count=%d err=%v", rolledBack, err)
	}
	if _, replayed, err := repository.Execute(ctx, retryKey, func(ctx context.Context) (idempotency.Result, error) {
		_, err := postgres.Executor(ctx, pool).Exec(ctx, `INSERT INTO idempotency_probe(value) VALUES('retried')`)
		return idempotency.Result{Status: 201, Body: []byte(`{}`)}, err
	}); err != nil || replayed {
		t.Fatalf("503 retry replayed=%v err=%v", replayed, err)
	}

	fakeClock.Advance(24*time.Hour + time.Nanosecond)
	if _, replayed, err := repository.Execute(ctx, request, func(context.Context) (idempotency.Result, error) {
		executions.Add(1)
		return idempotency.Result{Status: 200, Body: []byte(`{"generation":2}`)}, nil
	}); err != nil || replayed || executions.Load() != 2 {
		t.Fatalf("expired generation replayed=%v executions=%d err=%v", replayed, executions.Load(), err)
	}
}

func TestInFlightDuplicateExecutesOnce(t *testing.T) {
	ctx := context.Background()
	pool := openTestPool(t, ctx)
	lockTestDatabase(t, ctx, pool)
	resetFoundation(t, ctx, pool)
	repository := postgres.NewIdempotencyRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)))
	request := idempotency.Request{Scope: idempotency.Scope{Type: "user_login", Key: "alice"}, Operation: "login", Key: uuid.New(), RequestHash: "same"}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var executions atomic.Int32
	callback := func(context.Context) (idempotency.Result, error) {
		executions.Add(1)
		once.Do(func() { close(started) })
		<-release
		return idempotency.Result{Status: 200, Body: []byte(`{"token":"redacted"}`)}, nil
	}
	type outcome struct {
		replayed bool
		err      error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		_, replayed, err := repository.Execute(ctx, request, callback)
		outcomes <- outcome{replayed, err}
	}()
	<-started
	go func() {
		_, replayed, err := repository.Execute(ctx, request, callback)
		outcomes <- outcome{replayed, err}
	}()
	close(release)
	first, second := <-outcomes, <-outcomes
	if first.err != nil || second.err != nil || executions.Load() != 1 || first.replayed == second.replayed {
		t.Fatalf("outcomes=%#v %#v executions=%d", first, second, executions.Load())
	}
}
