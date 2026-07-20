package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
)

func TestUS2RelationshipLifecycle(t *testing.T) {
	pool, _ := us1Database(t)
	repository := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(us1Now))
	a, b := uuid.New(), uuid.New()
	addUser(t, pool, a, "america")
	addUser(t, pool, b, "america")
	request, err := repository.CreateRequest(context.Background(), a, b)
	if err != nil || request.Status != domain.RequestPending {
		t.Fatalf("create=%#v err=%v", request, err)
	}
	if _, err := repository.TransitionRequest(context.Background(), a, request.ID, domain.RequestAccepted); !errors.Is(err, postgres.ErrRelationshipForbidden) {
		t.Fatalf("wrong actor accepted: %v", err)
	}
	accepted, err := repository.TransitionRequest(context.Background(), b, request.ID, domain.RequestAccepted)
	if err != nil || accepted.Status != domain.RequestAccepted {
		t.Fatalf("accept=%#v err=%v", accepted, err)
	}
	if err := repository.DeleteFriendship(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
	retry, err := repository.CreateRequest(context.Background(), a, b)
	if err != nil || retry.ID == request.ID {
		t.Fatalf("re-request=%#v err=%v", retry, err)
	}
	if _, err := repository.TransitionRequest(context.Background(), a, retry.ID, domain.RequestCancelled); err != nil {
		t.Fatal(err)
	}
}

func TestUS2MutualFollowAndBlockCleanup(t *testing.T) {
	pool, _ := us1Database(t)
	repository := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(us1Now))
	a, b := uuid.New(), uuid.New()
	addUser(t, pool, a, "america")
	addUser(t, pool, b, "america")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET account_type='certified_public' WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if err := repository.Follow(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
	first, err := repository.CreateRequest(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	mutual, err := repository.CreateRequest(context.Background(), b, a)
	if err != nil || mutual.Status != domain.RequestMutualized {
		t.Fatalf("mutual=%#v err=%v", mutual, err)
	}
	if err := repository.Block(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
	var friendships, follows, pending int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM friendships`).Scan(&friendships)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM follows`).Scan(&follows)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM friend_requests WHERE status='pending'`).Scan(&pending)
	if friendships+follows+pending != 0 {
		t.Fatalf("cleanup friendships=%d follows=%d pending=%d first=%s", friendships, follows, pending, first.ID)
	}
	if err := repository.Unblock(context.Background(), b, a); !errors.Is(err, postgres.ErrRelationshipForbidden) {
		t.Fatalf("non-actor unblock=%v", err)
	}
	if err := repository.Unblock(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
}
