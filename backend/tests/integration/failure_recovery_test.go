package integration_test

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/isucon/isucon2026/backend/internal/worker"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5"
)

func TestFailureRecoveryRemovesPublishedImageWhenDatabaseMutationRollsBack(t *testing.T) {
	pool, repository := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "asia")
	root := t.TempDir()
	store := isurealimage.NewStore(root, reencodeSanitizer{}, domain.RandomIDGenerator{})
	postService := service.NewPostService(repository, service.NewVisibilityPolicy(repository), store, clock.NewFake(us1Now), domain.RandomIDGenerator{})
	_, err := postService.Create(context.Background(), user, service.CreatePostInput{Image: bytes.NewReader(testJPEG(t, 8, 8)), Caption: "rollback", Visibility: domain.VisibilityFriends, Location: domain.NoLocation()})
	if err == nil {
		t.Fatal("post without a current window unexpectedly succeeded")
	}
	var files int
	if err := fs.WalkDir(osDirFS(root), ".", func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			files++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("published image survived rollback: %d files", files)
	}
}

// osDirFS keeps the test's filesystem walk rooted at the temporary image directory.
func osDirFS(root string) fs.FS { return os.DirFS(root) }

func TestFailureRecoveryWorkerRestartCompletesRetryableJob(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	fake := clock.NewFake(us1Now)
	jobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO worker_jobs(id,kind,payload,status,next_attempt_at,created_at,updated_at)VALUES($1,'probe','{}','pending',$2,$2,$2)`, jobID, us1Now); err != nil {
		t.Fatal(err)
	}
	failing := &worker.Runner{DB: &postgres.DB{Pool: pool}, Clock: fake, BatchSize: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handlers: map[string]worker.Handler{"probe": worker.HandlerFunc(func(context.Context, pgx.Tx, worker.Job) error { return context.DeadlineExceeded })}}
	if err := failing.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute)
	restarted := &worker.Runner{DB: &postgres.DB{Pool: pool}, Clock: fake, BatchSize: 1, Logger: failing.Logger, Handlers: map[string]worker.Handler{"probe": worker.HandlerFunc(func(context.Context, pgx.Tx, worker.Job) error { return nil })}}
	if err := restarted.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempts FROM worker_jobs WHERE id=$1`, jobID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || attempts != 2 {
		t.Fatalf("status=%s attempts=%d", status, attempts)
	}
}

func TestFailureRecoveryKeysetCursorDoesNotDependOnDeletedAnchor(t *testing.T) {
	ctx := context.Background()
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "asia")
	newWindow, oldWindow := uuid.New(), uuid.New()
	addWindow(t, pool, newWindow, "asia", true)
	if _, err := pool.Exec(ctx, `UPDATE posting_windows SET local_date='2026-07-20',starts_at=$2,on_time_deadline=$3 WHERE id=$1`, newWindow, us1Now, us1Now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	addWindow(t, pool, oldWindow, "europe", true)
	newID, oldID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO notifications(id,user_id,posting_window_id,region,created_at)VALUES($1,$2,$3,'asia',$5),($4,$2,$6,'europe',$7)`, newID, user, newWindow, oldID, us1Now, oldWindow, us1Now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewNotificationRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(us1Now))
	anchor := us1Now
	if _, err := pool.Exec(ctx, `DELETE FROM notifications WHERE id=$1`, newID); err != nil {
		t.Fatal(err)
	}
	records, err := repository.List(ctx, user, &anchor, &newID, 51)
	if err != nil || len(records) != 1 || records[0].ID != oldID {
		t.Fatalf("records=%v err=%v", records, err)
	}
}
