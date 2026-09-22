package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistrationLoginAndSessionReplacement(t *testing.T) {
	ctx := context.Background()
	pool := openTestPool(t, ctx)
	lockTestDatabase(t, ctx, pool)
	resetFoundation(t, ctx, pool)

	db := &postgres.DB{Pool: pool}
	repository := postgres.NewSessionRepository(db, domain.NewDeterministicIDGenerator(testNamespace), clock.NewFake(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)))
	sessions := service.NewSessionService(repository)

	registered, err := sessions.Register(ctx, service.RegistrationInput{Username: "Alice", DisplayName: "アリス", Region: "asia"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.Token == "" || registered.User.Username != "Alice" || registered.User.UsernameNormalized != "alice" {
		t.Fatalf("registered = %#v", registered)
	}

	if _, err := sessions.Register(ctx, service.RegistrationInput{Username: "ALICE", DisplayName: "Other", Region: "europe"}); !errors.Is(err, service.ErrUsernameTaken) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}

	missingErr := loginError(sessions, ctx, "missing", registered.SecretKey)
	wrongErr := loginError(sessions, ctx, "Alice", registered.SecretKey+"x")
	if !errors.Is(missingErr, service.ErrInvalidCredentials) || !errors.Is(wrongErr, service.ErrInvalidCredentials) || missingErr.Error() != wrongErr.Error() {
		t.Fatalf("login errors differ: missing=%v wrong=%v", missingErr, wrongErr)
	}

	if err := repository.InsertTestPushSubscription(ctx, registered.User.ID, registered.SessionID); err != nil {
		t.Fatal(err)
	}
	replacement, err := sessions.Login(ctx, service.LoginInput{Username: "aLiCe", SecretKey: registered.SecretKey})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := repository.AuthenticateUser(ctx, registered.Token); !errors.Is(err, service.ErrInvalidSession) {
		t.Fatalf("old session authentication error = %v", err)
	}
	if _, err := repository.AuthenticateUser(ctx, replacement.Token); err != nil {
		t.Fatalf("new session authentication: %v", err)
	}
	if active, err := repository.ActivePushSubscriptionCount(ctx, registered.User.ID); err != nil || active != 0 {
		t.Fatalf("active subscriptions = %d, %v", active, err)
	}

	if err := sessions.Logout(ctx, replacement.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := repository.AuthenticateUser(ctx, replacement.Token); !errors.Is(err, service.ErrInvalidSession) {
		t.Fatalf("logged-out session error = %v", err)
	}
}

func loginError(sessions *service.SessionService, ctx context.Context, username, secretKey string) error {
	_, err := sessions.Login(ctx, service.LoginInput{Username: username, SecretKey: secretKey})
	return err
}

func openTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("ISUREAL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ISUREAL_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func lockTestDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `SELECT pg_advisory_lock(20260719)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `SELECT pg_advisory_unlock(20260719)`) })
}
