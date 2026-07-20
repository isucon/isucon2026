package integration_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"testing"
	"time"
)

func interactionObject(owner uuid.UUID) isurealimage.Object {
	return isurealimage.Object{ID: uuid.New(), OwnerUserID: owner, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ObjectKey: uuid.NewString() + ".jpg", ByteSize: 10, MediaType: "image/jpeg"}
}

func TestUS3RealMojiImageBoundaries(t *testing.T) {
	store := isurealimage.NewStore(t.TempDir(), copySanitizer{}, domain.RandomIDGenerator{})
	payload := make([]byte, (2<<20)+1)
	payload[0], payload[1] = 0xff, 0xd8
	if _, err := store.Save(context.Background(), uuid.New(), isurealimage.RealMojiImage, bytes.NewReader(payload)); !errors.Is(err, isurealimage.ErrPayloadTooLarge) {
		t.Fatalf("over-limit error=%v", err)
	}
	if _, err := store.Save(context.Background(), uuid.New(), isurealimage.RealMojiImage, bytes.NewReader([]byte("not jpeg"))); !errors.Is(err, isurealimage.ErrUnsupportedMedia) {
		t.Fatalf("MIME error=%v", err)
	}
	clean := isurealimage.NewStore(t.TempDir(), reencodeSanitizer{}, domain.RandomIDGenerator{})
	if _, err := clean.Save(context.Background(), uuid.New(), isurealimage.RealMojiImage, bytes.NewReader(testJPEG(t, 2048, 1024))); err != nil {
		t.Fatalf("dimension-independent sanitized image: %v", err)
	}
}
func TestUS3InteractionLifecycle(t *testing.T) {
	pool, posts := us1Database(t)
	viewer, author := uuid.New(), uuid.New()
	addUser(t, pool, viewer, "america")
	addUser(t, pool, author, "america")
	addWindow(t, pool, uuid.New(), "america", false)
	addFriendship(t, pool, viewer, author)
	createPost(t, posts, viewer, "viewer", domain.VisibilityFriends, us1Now)
	post := createPost(t, posts, author, "author", domain.VisibilityFriends, us1Now)
	repo := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	first, err := repo.ReplaceRealMoji(context.Background(), viewer, post.Post.ID, interactionObject(viewer), domain.ReactionLike, uuid.New(), us1Now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ReplaceRealMoji(context.Background(), viewer, post.Post.ID, interactionObject(viewer), domain.ReactionHeart, uuid.New(), us1Now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	realmojis, err := repo.ListRealMojis(context.Background(), post.Post.ID, nil, nil, 51)
	if err != nil || len(realmojis) != 1 || realmojis[0].RealMoji.ID != second.RealMoji.ID {
		t.Fatalf("replacement=%#v err=%v first=%s", realmojis, err, first.RealMoji.ID)
	}
	c1, err := repo.CreateComment(context.Background(), viewer, post.Post.ID, uuid.New(), "one", us1Now)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := repo.CreateComment(context.Background(), author, post.Post.ID, uuid.New(), "self", us1Now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	comments, err := repo.ListComments(context.Background(), post.Post.ID, nil, nil, 51)
	if err != nil || len(comments) != 2 || comments[0].Comment.ID != c1.Comment.ID || comments[1].Comment.ID != c2.Comment.ID {
		t.Fatalf("comments=%#v err=%v", comments, err)
	}
	third := uuid.New()
	addUser(t, pool, third, "america")
	if err := repo.DeleteComment(context.Background(), third, c1.Comment.ID, us1Now.Add(2*time.Second)); !errors.Is(err, postgres.ErrInteractionForbidden) {
		t.Fatalf("third-party delete=%v", err)
	}
	if err := repo.DeleteComment(context.Background(), author, c1.Comment.ID, us1Now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteRealMoji(context.Background(), viewer, second.RealMoji.ID, us1Now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
}
func TestUS3EndedAndDistanceTwoPolicies(t *testing.T) {
	pool, posts := us1Database(t)
	viewer, middle, author := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, middle, author} {
		addUser(t, pool, id, "america")
	}
	window := uuid.New()
	addWindow(t, pool, window, "america", false)
	addFriendship(t, pool, viewer, middle)
	addFriendship(t, pool, middle, author)
	createPost(t, posts, viewer, "viewer", domain.VisibilityFriends, us1Now)
	post := createPost(t, posts, author, "author", domain.VisibilityFriendsOfFriends, us1Now)
	repo := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	if _, err := repo.ReplaceRealMoji(context.Background(), viewer, post.Post.ID, interactionObject(viewer), domain.ReactionSmile, uuid.New(), us1Now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateComment(context.Background(), viewer, post.Post.ID, uuid.New(), "denied", us1Now); !errors.Is(err, postgres.ErrInteractionForbidden) {
		t.Fatalf("distance-two comment=%v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE posting_windows SET ended_at=$2 WHERE id=$1`, window, us1Now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReplaceRealMoji(context.Background(), viewer, post.Post.ID, interactionObject(viewer), domain.ReactionLaugh, uuid.New(), us1Now.Add(2*time.Minute)); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("ended interaction=%v", err)
	}
}
