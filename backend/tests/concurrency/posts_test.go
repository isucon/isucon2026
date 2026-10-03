package concurrency_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

var concurrencyNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func TestUS1PostConcurrencyScenarios(t *testing.T) {
	pool := testkit.Database(t)
	t.Run("one active post under concurrent create", func(t *testing.T) {
		testkit.Reset(t, pool)
		repo, user := concurrencyFixture(t, pool)
		errorsOut := make(chan error, 2)
		var wait sync.WaitGroup
		for index := 0; index < 2; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := repo.Create(context.Background(), user, postObject(user), "race", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow, uuid.New())
				errorsOut <- err
			}()
		}
		wait.Wait()
		close(errorsOut)
		success, conflicts := 0, 0
		for err := range errorsOut {
			if err == nil {
				success++
			} else if errors.Is(err, postgres.ErrPostSlotClosed) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflicts != 1 {
			t.Fatalf("success=%d conflicts=%d", success, conflicts)
		}
	})
	t.Run("first delete permits one repost", func(t *testing.T) {
		testkit.Reset(t, pool)
		repo, user := concurrencyFixture(t, pool)
		first := mustCreate(t, repo, user)
		if _, err := repo.Delete(context.Background(), user, first.ID, concurrencyNow.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(context.Background(), user, postObject(user), "second", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow.Add(2*time.Second), uuid.New()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("second delete wins against third post", func(t *testing.T) {
		testkit.Reset(t, pool)
		repo, user := concurrencyFixture(t, pool)
		first := mustCreate(t, repo, user)
		if _, err := repo.Delete(context.Background(), user, first.ID, concurrencyNow.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		second := mustCreate(t, repo, user)
		if _, err := repo.Delete(context.Background(), user, second.ID, concurrencyNow.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(context.Background(), user, postObject(user), "third", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow.Add(3*time.Second), uuid.New()); !errors.Is(err, postgres.ErrPostSlotClosed) {
			t.Fatalf("third create error=%v", err)
		}
	})
	t.Run("idempotent create retry", func(t *testing.T) {
		testkit.Reset(t, pool)
		repo, user := concurrencyFixture(t, pool)
		store := postgres.NewIdempotencyRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		request := idempotency.Request{Scope: idempotency.Scope{Type: "user", Key: user.String()}, Operation: "create_post", Key: uuid.New(), RequestHash: "same-create"}
		var executions atomic.Int32
		callback := func(ctx context.Context) (idempotency.Result, error) {
			executions.Add(1)
			_, err := repo.Create(ctx, user, postObject(user), "once", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow, uuid.New())
			return idempotency.Result{Status: http.StatusCreated, Body: []byte(`{"ok":true}`)}, err
		}
		if _, replayed, err := store.Execute(context.Background(), request, callback); err != nil || replayed {
			t.Fatalf("first replay=%v err=%v", replayed, err)
		}
		if _, replayed, err := store.Execute(context.Background(), request, callback); err != nil || !replayed || executions.Load() != 1 {
			t.Fatalf("retry replay=%v executions=%d err=%v", replayed, executions.Load(), err)
		}
	})
	t.Run("idempotent delete retry", func(t *testing.T) {
		testkit.Reset(t, pool)
		repo, user := concurrencyFixture(t, pool)
		post := mustCreate(t, repo, user)
		store := postgres.NewIdempotencyRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		request := idempotency.Request{Scope: idempotency.Scope{Type: "user", Key: user.String()}, Operation: "delete_post", Key: uuid.New(), RequestHash: "same-delete"}
		var executions atomic.Int32
		callback := func(ctx context.Context) (idempotency.Result, error) {
			executions.Add(1)
			_, err := repo.Delete(ctx, user, post.ID, concurrencyNow.Add(time.Second))
			return idempotency.Result{Status: http.StatusNoContent}, err
		}
		if _, _, err := store.Execute(context.Background(), request, callback); err != nil {
			t.Fatal(err)
		}
		if _, replayed, err := store.Execute(context.Background(), request, callback); err != nil || !replayed || executions.Load() != 1 {
			t.Fatalf("retry replay=%v executions=%d err=%v", replayed, executions.Load(), err)
		}
	})
}

func concurrencyFixture(t *testing.T, pool *pgxpool.Pool) (*postgres.PostRepository, uuid.UUID) {
	t.Helper()
	user, window := uuid.New(), uuid.New()
	name := "u" + user.String()[:8]
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,username,username_normalized,secret_key_hash,display_name,display_name_search,region) VALUES($1,$2::varchar,$2::varchar,'hash',$2::text,$2::text,'america')`, user, name); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline) VALUES($1,'america','2026-07-19',-300,$2,$3)`, window, concurrencyNow.Add(-time.Minute), concurrencyNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return postgres.NewPostRepository(&postgres.DB{Pool: pool}), user
}
func postObject(user uuid.UUID) isurealimage.Object {
	return isurealimage.Object{ID: uuid.New(), OwnerUserID: user, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObjectKey: uuid.NewString() + ".jpg", ByteSize: 10, MediaType: "image/jpeg"}
}
func mustCreate(t *testing.T, repo *postgres.PostRepository, user uuid.UUID) domain.Post {
	t.Helper()
	post, err := repo.Create(context.Background(), user, postObject(user), "post", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return post
}
