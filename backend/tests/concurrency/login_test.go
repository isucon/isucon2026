package concurrency_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentLoginLeavesOneActiveSession(t *testing.T) {
	ctx := context.Background()
	url := os.Getenv("ISUREAL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ISUREAL_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SELECT pg_advisory_lock(20260719)`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `SELECT pg_advisory_unlock(20260719)`)
	_, filename, _, _ := runtime.Caller(0)
	if err := schema.Reset(ctx, pool, filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql")); err != nil {
		t.Fatal(err)
	}

	db := &postgres.DB{Pool: pool}
	repository := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, clock.NewFake(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)))
	sessions := service.NewSessionService(repository)
	registered, err := sessions.Register(ctx, service.RegistrationInput{Username: "racer", DisplayName: "Racer", Region: "america"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertTestPushSubscription(ctx, registered.User.ID, registered.SessionID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan service.SessionResult, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := sessions.Login(ctx, service.LoginInput{Username: "RACER", SecretKey: registered.SecretKey})
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Errorf("login: %v", err)
	}
	if count := len(results); count != 2 {
		t.Fatalf("successful logins = %d, want 2 serialized successes", count)
	}
	active, err := repository.ActiveSessionCount(ctx, registered.User.ID)
	if err != nil || active != 1 {
		t.Fatalf("active sessions = %d, %v", active, err)
	}
	pushes, err := repository.ActivePushSubscriptionCount(ctx, registered.User.ID)
	if err != nil || pushes != 0 {
		t.Fatalf("active subscriptions = %d, %v", pushes, err)
	}

	validTokens := 0
	for result := range results {
		if _, err := repository.AuthenticateUser(ctx, result.Token); err == nil {
			validTokens++
		}
	}
	if validTokens != 1 {
		t.Fatalf("valid returned tokens = %d, want 1", validTokens)
	}
}
