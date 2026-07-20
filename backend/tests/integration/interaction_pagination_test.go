package integration_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"testing"
	"time"
)

func TestUS3InteractionPagination(t *testing.T) {
	pool, posts := us1Database(t)
	viewer, author := uuid.New(), uuid.New()
	addUser(t, pool, viewer, "america")
	addUser(t, pool, author, "america")
	addWindow(t, pool, uuid.New(), "america", false)
	addFriendship(t, pool, viewer, author)
	createPost(t, posts, viewer, "viewer", domain.VisibilityFriends, us1Now)
	post := createPost(t, posts, author, "author", domain.VisibilityFriends, us1Now)
	repo := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	for index := 0; index < 51; index++ {
		if _, err := repo.CreateComment(context.Background(), viewer, post.Post.ID, uuid.New(), "item", us1Now.Add(time.Duration(index)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ListComments(context.Background(), post.Post.ID, nil, nil, 51)
	if err != nil || len(first) != 51 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	at, id := first[49].Comment.CreatedAt, first[49].Comment.ID
	tail, err := repo.ListComments(context.Background(), post.Post.ID, &at, &id, 51)
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail=%d err=%v", len(tail), err)
	}
	if tail[0].Comment.ID == first[49].Comment.ID {
		t.Fatal("duplicate cursor boundary")
	}
}

func TestUS3RealMojiPagination(t *testing.T) {
	pool, posts := us1Database(t)
	author := uuid.New()
	addUser(t, pool, author, "america")
	addWindow(t, pool, uuid.New(), "america", false)
	post := createPost(t, posts, author, "author", domain.VisibilityPublic, us1Now)
	for index := 0; index < 51; index++ {
		reactor, imageID, realID := uuid.New(), uuid.New(), uuid.New()
		addUser(t, pool, reactor, "america")
		if _, err := pool.Exec(context.Background(), `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at)VALUES($1,$2,$3,$4,10,'image/jpeg',$5)`, imageID, reactor, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", imageID.String()+".jpg", us1Now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at)VALUES($1,$2,$3,$4,'like',$5)`, realID, post.Post.ID, reactor, imageID, us1Now.Add(time.Duration(index)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	repo := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	first, err := repo.ListRealMojis(context.Background(), post.Post.ID, nil, nil, 51)
	if err != nil || len(first) != 51 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	at, id := first[49].RealMoji.CreatedAt, first[49].RealMoji.ID
	tail, err := repo.ListRealMojis(context.Background(), post.Post.ID, &at, &id, 51)
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail=%d err=%v", len(tail), err)
	}
}
