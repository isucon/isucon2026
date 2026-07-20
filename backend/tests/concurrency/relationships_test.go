package concurrency_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUS2RelationshipConcurrency(t *testing.T) {
	pool := testkit.Database(t)
	t.Run("mutual requests create one friendship", func(t *testing.T) {
		testkit.Reset(t, pool)
		a, b := relationUser(t, pool), relationUser(t, pool)
		repo := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		start := make(chan struct{})
		out := make(chan error, 2)
		var wait sync.WaitGroup
		for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
			wait.Add(1)
			go func(left, right uuid.UUID) {
				defer wait.Done()
				<-start
				_, err := repo.CreateRequest(context.Background(), left, right)
				out <- err
			}(pair[0], pair[1])
		}
		close(start)
		wait.Wait()
		close(out)
		for err := range out {
			if err != nil && !errors.Is(err, postgres.ErrRelationshipConflict) {
				t.Fatal(err)
			}
		}
		var friendships, pending int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM friendships`).Scan(&friendships)
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM friend_requests WHERE status='pending'`).Scan(&pending)
		if friendships != 1 || pending != 0 {
			t.Fatalf("friendships=%d pending=%d", friendships, pending)
		}
	})
	t.Run("block wins against follow and request", func(t *testing.T) {
		testkit.Reset(t, pool)
		a, b := relationUser(t, pool), relationUser(t, pool)
		if _, err := pool.Exec(context.Background(), `UPDATE users SET account_type='certified_public' WHERE id=$1`, b); err != nil {
			t.Fatal(err)
		}
		repo := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		if _, err := repo.CreateRequest(context.Background(), a, b); err != nil {
			t.Fatal(err)
		}
		if err := repo.Follow(context.Background(), a, b); err != nil {
			t.Fatal(err)
		}
		if err := repo.Block(context.Background(), b, a); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateRequest(context.Background(), a, b); !errors.Is(err, postgres.ErrRelationshipConflict) {
			t.Fatalf("blocked request=%v", err)
		}
		if err := repo.Follow(context.Background(), a, b); !errors.Is(err, postgres.ErrRelationshipConflict) {
			t.Fatalf("blocked follow=%v", err)
		}
	})
	t.Run("duplicate certification changes once", func(t *testing.T) {
		testkit.Reset(t, pool)
		user := relationUser(t, pool)
		repo := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		out := make(chan error, 2)
		go func() { out <- repo.Certify(context.Background(), user) }()
		go func() { out <- repo.Certify(context.Background(), user) }()
		success, conflict := 0, 0
		for index := 0; index < 2; index++ {
			err := <-out
			if err == nil {
				success++
			} else if errors.Is(err, postgres.ErrRelationshipConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("success=%d conflict=%d", success, conflict)
		}
	})
	t.Run("accept versus cancel and double removal", func(t *testing.T) {
		testkit.Reset(t, pool)
		a, b := relationUser(t, pool), relationUser(t, pool)
		repo := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(concurrencyNow))
		request, err := repo.CreateRequest(context.Background(), a, b)
		if err != nil {
			t.Fatal(err)
		}
		out := make(chan error, 2)
		go func() {
			_, err := repo.TransitionRequest(context.Background(), b, request.ID, domain.RequestAccepted)
			out <- err
		}()
		go func() {
			_, err := repo.TransitionRequest(context.Background(), a, request.ID, domain.RequestCancelled)
			out <- err
		}()
		success := 0
		for index := 0; index < 2; index++ {
			if err := <-out; err == nil {
				success++
			}
		}
		if success != 1 {
			t.Fatalf("transition successes=%d", success)
		}
		var friendship int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM friendships`).Scan(&friendship)
		if friendship == 1 {
			deletes := make(chan error, 2)
			go func() { deletes <- repo.DeleteFriendship(context.Background(), a, b) }()
			go func() { deletes <- repo.DeleteFriendship(context.Background(), b, a) }()
			deleteSuccess := 0
			for index := 0; index < 2; index++ {
				if err := <-deletes; err == nil {
					deleteSuccess++
				}
			}
			if deleteSuccess != 1 {
				t.Fatalf("delete successes=%d", deleteSuccess)
			}
		}
	})
}
func relationUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	name := "u" + id.String()[:8]
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,username,username_normalized,password_hash,display_name,display_name_search,region)VALUES($1,$2::varchar,$2::varchar,'hash',$2::text,$2::text,'america')`, id, name); err != nil {
		t.Fatal(err)
	}
	return id
}
