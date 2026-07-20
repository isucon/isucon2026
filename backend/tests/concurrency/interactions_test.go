package concurrency_test

import "time"

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUS3InteractionConcurrency(t *testing.T) {
	pool := testkit.Database(t)
	t.Run("simultaneous replacement leaves one active", func(t *testing.T) {
		testkit.Reset(t, pool)
		posts, interactions, viewer, post := interactionFixture(t, pool)
		_ = posts
		out := make(chan error, 2)
		var wait sync.WaitGroup
		for index := 0; index < 2; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := interactions.ReplaceRealMoji(context.Background(), viewer, post, interactionTestObject(viewer), domain.ReactionHeart, uuid.New(), concurrencyNow)
				out <- err
			}()
		}
		wait.Wait()
		close(out)
		for err := range out {
			if err != nil {
				t.Fatal(err)
			}
		}
		var active int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM realmojis WHERE replaced_at IS NULL AND deleted_at IS NULL`).Scan(&active); err != nil || active != 1 {
			t.Fatalf("active=%d err=%v", active, err)
		}
	})
	t.Run("post delete versus create leaves no visible interaction", func(t *testing.T) {
		testkit.Reset(t, pool)
		posts, interactions, viewer, post := interactionFixture(t, pool)
		out := make(chan error, 2)
		go func() {
			_, err := interactions.CreateComment(context.Background(), viewer, post, uuid.New(), "race", concurrencyNow)
			out <- err
		}()
		go func() {
			_, err := posts.Delete(context.Background(), postAuthor(t, pool, post), post, concurrencyNow)
			out <- err
		}()
		<-out
		<-out
		var visible int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM comments c JOIN posts p ON p.id=c.post_id WHERE c.deleted_at IS NULL AND p.deleted_at IS NULL`).Scan(&visible); err != nil || visible != 0 {
			t.Fatalf("visible=%d err=%v", visible, err)
		}
	})
	t.Run("duplicate comment retry executes once", func(t *testing.T) {
		testkit.Reset(t, pool)
		_, interactions, viewer, post := interactionFixture(t, pool)
		store := postgres.NewIdempotencyRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		request := idempotency.Request{Scope: idempotency.Scope{Type: "user", Key: viewer.String()}, Operation: "create_comment", Key: uuid.New(), RequestHash: "same"}
		var calls atomic.Int32
		callback := func(ctx context.Context) (idempotency.Result, error) {
			calls.Add(1)
			_, err := interactions.CreateComment(ctx, viewer, post, uuid.New(), "once", concurrencyNow)
			return idempotency.Result{Status: http.StatusCreated, Body: []byte(`{}`)}, err
		}
		if _, _, err := store.Execute(context.Background(), request, callback); err != nil {
			t.Fatal(err)
		}
		if _, replayed, err := store.Execute(context.Background(), request, callback); err != nil || !replayed || calls.Load() != 1 {
			t.Fatalf("replayed=%v calls=%d err=%v", replayed, calls.Load(), err)
		}
	})
	t.Run("block prevents later interaction", func(t *testing.T) {
		testkit.Reset(t, pool)
		_, interactions, viewer, post := interactionFixture(t, pool)
		author := postAuthor(t, pool, post)
		relations := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		if err := relations.Block(context.Background(), author, viewer); err != nil {
			t.Fatal(err)
		}
		if _, err := interactions.CreateComment(context.Background(), viewer, post, uuid.New(), "blocked", concurrencyNow); !errors.Is(err, postgres.ErrInteractionForbidden) {
			t.Fatalf("blocked comment=%v", err)
		}
	})
}
func interactionFixture(t *testing.T, pool *pgxpool.Pool) (*postgres.PostRepository, *postgres.InteractionRepository, uuid.UUID, uuid.UUID) {
	t.Helper()
	viewer, author := relationUser(t, pool), relationUser(t, pool)
	window := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline)VALUES($1,'america','2026-07-19',-300,$2,$3)`, window, concurrencyNow.Add(-time.Minute), concurrencyNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	left, right := viewer, author
	if left.String() > right.String() {
		left, right = right, left
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO friendships(id,user_low_id,user_high_id)VALUES($1,$2,$3)`, uuid.New(), left, right); err != nil {
		t.Fatal(err)
	}
	posts := postgres.NewPostRepository(&postgres.DB{Pool: pool})
	if _, err := posts.Create(context.Background(), viewer, postObject(viewer), "viewer", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow, uuid.New()); err != nil {
		t.Fatal(err)
	}
	created, err := posts.Create(context.Background(), author, postObject(author), "author", domain.VisibilityFriends, domain.NoLocation(), concurrencyNow, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return posts, postgres.NewInteractionRepository(&postgres.DB{Pool: pool}), viewer, created.ID
}
func interactionTestObject(owner uuid.UUID) isurealimage.Object {
	return isurealimage.Object{ID: uuid.New(), OwnerUserID: owner, SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ObjectKey: uuid.NewString() + ".jpg", ByteSize: 10, MediaType: "image/jpeg"}
}
func postAuthor(t *testing.T, pool *pgxpool.Pool, post uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT author_id FROM posts WHERE id=$1`, post).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
