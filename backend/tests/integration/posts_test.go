package integration_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
)

func TestUS1PostIntegrationScenarios(t *testing.T) {
	t.Run("post before feed gate", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, author := uuid.New(), uuid.New()
		addUser(t, pool, viewer, "america")
		addUser(t, pool, author, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		post := createPost(t, repo, author, "public", domain.VisibilityPublic, us1Now)
		facts, err := repo.VisibilityFacts(context.Background(), viewer, post.Post.ID)
		if err != nil {
			t.Fatal(err)
		}
		if facts.ViewerPosted {
			t.Fatal("viewer-posted gate unexpectedly open")
		}
	})
	t.Run("cross-region viewer and poster windows", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, author := uuid.New(), uuid.New()
		addUser(t, pool, viewer, "europe")
		addUser(t, pool, author, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		addWindow(t, pool, uuid.New(), "europe", false)
		viewerPost := createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		_ = viewerPost
		authorPost := createPost(t, repo, author, "author", domain.VisibilityPublic, us1Now)
		allowed, err := service.NewVisibilityPolicy(repo).CanViewPost(context.Background(), viewer, authorPost.Post.ID)
		if err != nil || !allowed {
			t.Fatalf("cross-region post hidden: %v", err)
		}
	})
	t.Run("friends visibility", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, author := uuid.New(), uuid.New()
		addUser(t, pool, viewer, "america")
		addUser(t, pool, author, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		addFriendship(t, pool, viewer, author)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		post := createPost(t, repo, author, "friend", domain.VisibilityFriends, us1Now)
		allowed, err := service.NewVisibilityPolicy(repo).CanViewPost(context.Background(), viewer, post.Post.ID)
		if err != nil || !allowed {
			t.Fatalf("friend post hidden: %v", err)
		}
	})
	t.Run("friends-of-friends visibility", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, middle, author := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{viewer, middle, author} {
			addUser(t, pool, id, "america")
		}
		addWindow(t, pool, uuid.New(), "america", false)
		addFriendship(t, pool, viewer, middle)
		addFriendship(t, pool, middle, author)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		post := createPost(t, repo, author, "fof", domain.VisibilityFriendsOfFriends, us1Now)
		allowed, err := service.NewVisibilityPolicy(repo).CanViewPost(context.Background(), viewer, post.Post.ID)
		if err != nil || !allowed {
			t.Fatalf("distance-two post hidden: %v", err)
		}
	})
	t.Run("public visibility", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, author := uuid.New(), uuid.New()
		addUser(t, pool, viewer, "america")
		addUser(t, pool, author, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		post := createPost(t, repo, author, "public", domain.VisibilityPublic, us1Now)
		allowed, err := service.NewVisibilityPolicy(repo).CanViewPost(context.Background(), viewer, post.Post.ID)
		if err != nil || !allowed {
			t.Fatalf("public post hidden: %v", err)
		}
	})
	t.Run("self excluded from four feeds", func(t *testing.T) {
		pool, repo := us1Database(t)
		user := uuid.New()
		addUser(t, pool, user, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, user, "self", domain.VisibilityPublic, us1Now)
		for _, kind := range []domain.FeedKind{domain.FeedFriends, domain.FeedFriendsOfFriends, domain.FeedPublic, domain.FeedFollowing} {
			records, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: user, Kind: kind, Limit: 51})
			if err != nil || len(records) != 0 {
				t.Fatalf("%s self results=%d err=%v", kind, len(records), err)
			}
		}
	})
	t.Run("caption and visibility updates preserve immutable fields", func(t *testing.T) {
		pool, repo := us1Database(t)
		user := uuid.New()
		addUser(t, pool, user, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		before := createPost(t, repo, user, "before", domain.VisibilityFriends, us1Now)
		caption := "after"
		visibility := domain.VisibilityPublic
		after, err := repo.Update(context.Background(), user, before.Post.ID, &caption, &visibility)
		if err != nil {
			t.Fatal(err)
		}
		if after.Post.ImageID != before.Post.ImageID || after.Post.Location.Kind != before.Post.Location.Kind || !after.Post.PostedAt.Equal(before.Post.PostedAt) {
			t.Fatal("immutable post fields changed")
		}
	})
	t.Run("ended-window update denial", func(t *testing.T) {
		pool, repo := us1Database(t)
		user := uuid.New()
		addUser(t, pool, user, "america")
		window := uuid.New()
		addWindow(t, pool, window, "america", false)
		post := createPost(t, repo, user, "before", domain.VisibilityFriends, us1Now)
		_, err := pool.Exec(context.Background(), `UPDATE posting_windows SET ended_at=$2 WHERE id=$1`, window, us1Now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		caption := "after"
		_, err = repo.Update(context.Background(), user, post.Post.ID, &caption, nil)
		if !errors.Is(err, postgres.ErrPostNotEditable) {
			t.Fatalf("update error=%v", err)
		}
	})
	t.Run("owner may delete ended post", func(t *testing.T) {
		pool, repo := us1Database(t)
		user := uuid.New()
		addUser(t, pool, user, "america")
		window := uuid.New()
		addWindow(t, pool, window, "america", false)
		post := createPost(t, repo, user, "old", domain.VisibilityFriends, us1Now)
		if _, err := pool.Exec(context.Background(), `UPDATE posting_windows SET ended_at=$2 WHERE id=$1`, window, us1Now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Delete(context.Background(), user, post.Post.ID, us1Now.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Get(context.Background(), post.Post.ID); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("deleted post error=%v", err)
		}
	})
	t.Run("caption over 1000 code points", func(t *testing.T) {
		if domain.ValidateCaption(string(bytes.Repeat([]byte("界"), 1001))) == nil {
			t.Fatal("oversized caption accepted")
		}
	})
	t.Run("JPEG MIME boundary", func(t *testing.T) {
		store := isurealimage.NewStore(t.TempDir(), copySanitizer{}, domain.RandomIDGenerator{})
		if _, err := store.Save(context.Background(), uuid.New(), isurealimage.PostImage, bytes.NewReader([]byte("not jpeg"))); !errors.Is(err, isurealimage.ErrUnsupportedMedia) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("JPEG exact 10 MiB and over-limit", func(t *testing.T) {
		store := isurealimage.NewStore(t.TempDir(), copySanitizer{}, domain.RandomIDGenerator{})
		payload := make([]byte, (10<<20)+1)
		payload[0], payload[1] = 0xff, 0xd8
		if _, err := store.Save(context.Background(), uuid.New(), isurealimage.PostImage, bytes.NewReader(payload)); !errors.Is(err, isurealimage.ErrPayloadTooLarge) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("metadata stripping", func(t *testing.T) {
		root := t.TempDir()
		store := isurealimage.NewStore(root, reencodeSanitizer{}, domain.RandomIDGenerator{})
		object, err := store.Save(context.Background(), uuid.New(), isurealimage.PostImage, bytes.NewReader(testJPEG(t, 32, 32)))
		if err != nil {
			t.Fatal(err)
		}
		file, err := store.Open(object.ObjectKey)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, _, err := image.Decode(file); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("large dimensions accepted", func(t *testing.T) {
		store := isurealimage.NewStore(t.TempDir(), reencodeSanitizer{}, domain.RandomIDGenerator{})
		if _, err := store.Save(context.Background(), uuid.New(), isurealimage.PostImage, bytes.NewReader(testJPEG(t, 2048, 1024))); err != nil {
			t.Fatal(err)
		}
	})
}

type copySanitizer struct{}

func (copySanitizer) Sanitize(_ context.Context, input io.Reader, output io.Writer) error {
	_, err := io.Copy(output, input)
	return err
}

type reencodeSanitizer struct{}

func (reencodeSanitizer) Sanitize(_ context.Context, input io.Reader, output io.Writer) error {
	decoded, _, err := image.Decode(input)
	if err != nil {
		return err
	}
	return jpeg.Encode(output, decoded, &jpeg.Options{Quality: 85})
}
func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	picture.Set(0, 0, color.White)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, picture, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
