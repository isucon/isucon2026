package integration_test

import "time"
import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"testing"
)

func TestUS4DeletionRestorePurgeAndUsernameReuse(t *testing.T) {
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "america")
	hash, err := service.HashPassword("password-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET password_hash=$2 WHERE id=$1`, user, hash); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(us1Now)
	db := &postgres.DB{Pool: pool}
	sessions := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, fake)
	accounts := postgres.NewAccountRepository(db, fake)
	due, err := accounts.RequestDeletion(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM users WHERE id=$1`, user).Scan(&status)
	if status != "deletion_pending" {
		t.Fatal("account remained active")
	}
	sessionService := service.NewSessionService(sessions)
	if _, err := sessionService.Login(context.Background(), service.LoginInput{Username: "u" + user.String()[:8], Password: "password-1"}); err != nil {
		t.Fatalf("restore login=%v", err)
	}
	_ = pool.QueryRow(context.Background(), `SELECT status FROM users WHERE id=$1`, user).Scan(&status)
	if status != "active" {
		t.Fatal("account not restored")
	}
	if _, err := accounts.RequestDeletion(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	fake.Set(due.Add(time.Nanosecond))
	keys, err := accounts.Purge(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	_ = keys
	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE id=$1`, user).Scan(&count)
	if count != 0 {
		t.Fatal("account not purged")
	}
	oldName := "u" + user.String()[:8]
	replacement := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,username,username_normalized,password_hash,display_name,display_name_search,region)VALUES($1,$2::varchar,$2::varchar,'hash',$2::text,$2::text,'america')`, replacement, oldName); err != nil {
		t.Fatalf("username was not released: %v", err)
	}
}

func TestUS4PurgeRemovesRelatedInteractionsAndImages(t *testing.T) {
	pool, posts := us1Database(t)
	owner, reactor := uuid.New(), uuid.New()
	addUser(t, pool, owner, "america")
	addUser(t, pool, reactor, "america")
	addWindow(t, pool, uuid.New(), "america", false)
	addFriendship(t, pool, owner, reactor)
	createPost(t, posts, reactor, "reactor", domain.VisibilityFriends, us1Now)
	post := createPost(t, posts, owner, "owner", domain.VisibilityFriends, us1Now)
	interactions := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	real, err := interactions.ReplaceRealMoji(context.Background(), reactor, post.Post.ID, interactionObject(reactor), domain.ReactionLike, uuid.New(), us1Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := interactions.CreateComment(context.Background(), reactor, post.Post.ID, uuid.New(), "comment", us1Now); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(us1Now)
	accounts := postgres.NewAccountRepository(&postgres.DB{Pool: pool}, fake)
	if _, err := accounts.RequestDeletion(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	fake.Advance(domain.AccountDeletionGrace)
	keys, err := accounts.Purge(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) < 2 {
		t.Fatalf("purged image keys=%d", len(keys))
	}
	var remaining int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM image_objects WHERE id=$1`, real.RealMoji.ImageID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("RealMoji image related to purged post remains")
	}
}
