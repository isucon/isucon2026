package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
)

func TestUS1FeedPaginationScenarios(t *testing.T) {
	t.Run("51 items use two pages", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer := uuid.New()
		addUser(t, pool, viewer, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		for index := 0; index < 51; index++ {
			author := uuid.New()
			addUser(t, pool, author, "america")
			createPost(t, repo, author, "item", domain.VisibilityPublic, us1Now.Add(-time.Duration(index)*time.Second))
		}
		first, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedPublic, Limit: 51})
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 51 {
			t.Fatalf("first candidate count=%d", len(first))
		}
		cutoffTime, cutoffID := first[49].Post.PostedAt, first[49].Post.ID
		second, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedPublic, BeforePostedAt: &cutoffTime, BeforeID: &cutoffID, Limit: 51})
		if err != nil {
			t.Fatal(err)
		}
		if len(second) != 1 {
			t.Fatalf("second page count=%d", len(second))
		}
	})
	t.Run("equal timestamps use descending IDs", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer := uuid.New()
		addUser(t, pool, viewer, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		for index := 0; index < 3; index++ {
			author := uuid.New()
			addUser(t, pool, author, "america")
			createPost(t, repo, author, "same", domain.VisibilityPublic, us1Now)
		}
		records, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedPublic, Limit: 51})
		if err != nil {
			t.Fatal(err)
		}
		for index := 1; index < len(records); index++ {
			if records[index-1].Post.ID.String() <= records[index].Post.ID.String() {
				t.Fatal("post IDs are not descending for equal timestamps")
			}
		}
	})
	t.Run("concurrent insert does not duplicate", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer := uuid.New()
		addUser(t, pool, viewer, "america")
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		for index := 0; index < 3; index++ {
			author := uuid.New()
			addUser(t, pool, author, "america")
			createPost(t, repo, author, "old", domain.VisibilityPublic, us1Now.Add(-time.Duration(index+1)*time.Second))
		}
		first, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedPublic, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		newAuthor := uuid.New()
		addUser(t, pool, newAuthor, "america")
		createPost(t, repo, newAuthor, "new", domain.VisibilityPublic, us1Now)
		cutoffTime, cutoffID := first[1].Post.PostedAt, first[1].Post.ID
		second, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedPublic, BeforePostedAt: &cutoffTime, BeforeID: &cutoffID, Limit: 51})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[uuid.UUID]bool{}
		for _, record := range append(first, second...) {
			if seen[record.Post.ID] {
				t.Fatal("duplicate across pages")
			}
			seen[record.Post.ID] = true
		}
		if len(seen) != 3 {
			t.Fatalf("stable traversal count=%d", len(seen))
		}
	})
	t.Run("exact distance two excludes direct friends", func(t *testing.T) {
		pool, repo := us1Database(t)
		viewer, middle, direct := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{viewer, middle, direct} {
			addUser(t, pool, id, "america")
		}
		addWindow(t, pool, uuid.New(), "america", false)
		createPost(t, repo, viewer, "viewer", domain.VisibilityFriends, us1Now)
		addFriendship(t, pool, viewer, middle)
		addFriendship(t, pool, middle, direct)
		addFriendship(t, pool, viewer, direct)
		createPost(t, repo, direct, "direct", domain.VisibilityFriendsOfFriends, us1Now)
		records, err := repo.ListFeed(context.Background(), domain.FeedQuery{ViewerID: viewer, Kind: domain.FeedFriendsOfFriends, Limit: 51})
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 0 {
			t.Fatal("direct friend leaked into exact-distance-two feed")
		}
	})
	t.Run("signed cursor cannot be reused across feeds", func(t *testing.T) {
		codec, err := httpapi.NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		token, err := codec.Encode(httpapi.Cursor{List: "feed:friends", Direction: httpapi.Descending, Values: []string{us1Now.Format(time.RFC3339Nano), uuid.NewString()}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := codec.Decode(token, "feed:public", httpapi.Descending); !errors.Is(err, httpapi.ErrCursorBinding) {
			t.Fatalf("cross-feed cursor error=%v", err)
		}
	})
}
