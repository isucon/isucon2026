package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func optimizedTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx-auth", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOptimizedVisibilityMatchesOriginal(t *testing.T) {
	db := optimizedTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	regions := []string{"asia", "europe", "america"}
	for i, region := range regions {
		exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline)
   VALUES($1::text,$1::text, date '2026-09-24' - $2::integer, '2026-09-24', '2026-09-25')`, region, i%2)
	}
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline,ended_at)
  VALUES('closed','asia','2026-09-23','2026-09-23','2026-09-24','2026-09-24')`)
	rng := rand.New(rand.NewSource(20260930))
	const users = 18
	for i := range users {
		id := fmt.Sprintf("u%02d", i)
		exec(`INSERT INTO users(id,username,display_name,region,secret_key_hash)
   VALUES($1::text,$1::text,$1::text,$2,repeat('ab',32))`, id, regions[i%3])
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1::text,$1::text,'photo','image/jpeg')`, id)
		// Empty and soft-deleted posting slots must not unlock a feed.
		if i%7 == 0 {
			continue
		}
		for j := range 15 {
			window := regions[(i+j)%3]
			if j%5 == 0 {
				window = "closed"
			}
			visibility := []string{"public", "friends", "friends_of_friends"}[rng.Intn(3)]
			exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at,deleted_at)
    VALUES($1::text,$2,$3,$2,$4,'none',timestamptz '2026-09-24' + ($5::integer * interval '1 second'),
      CASE WHEN $6::boolean THEN timestamptz '2026-09-25' END)`,
				fmt.Sprintf("%s-p%02d", id, j), id, window, visibility, j%3, i == 6 || j == 11)
		}
	}
	for i := range users {
		for j := i + 1; j < users; j++ {
			a, b := fmt.Sprintf("u%02d", i), fmt.Sprintf("u%02d", j)
			if rng.Intn(4) == 0 {
				exec(`INSERT INTO friendships(id,user_low_id,user_high_id) VALUES($1::text,$2,$3)`, a+b, a, b)
			}
			if rng.Intn(9) == 0 {
				// Exercise both directions, including blocked intermediate friends.
				exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES($1::text,$2,$3)`, a+b, b, a)
			}
			if rng.Intn(3) == 0 {
				exec(`INSERT INTO follows(id,follower_id,followee_id) VALUES($1::text,$2,$3)`, a+b, a, b)
			}
		}
	}
	exec(`UPDATE users SET status='deleted' WHERE id='u17'`)
	posts, err := selectPostsWithAuthor(ctx, db, `ORDER BY p.posted_at DESC,p.id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.GET("/api/feeds/:feed", getFeedHandler(&HandlerContext{DB: db}), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("session", &Session{UserID: c.Request().Header.Get("X-Test-Viewer")})
			return next(c)
		}
	})
	for i := range users - 1 {
		viewerID := fmt.Sprintf("u%02d", i)
		viewer, err := referenceViewerContext(ctx, db, viewerID)
		if err != nil {
			t.Fatal(err)
		}
		for _, post := range posts {
			actual, targeted, err := loadPostAccess(ctx, db, viewerID, post.ID)
			if err != nil {
				t.Fatal(err)
			}
			author := post.AuthorID
			if !reflect.DeepEqual(actual, post) {
				t.Fatalf("post mismatch: %s", post.ID)
			}
			if viewer.HasPostedToday != targeted.HasPostedToday || viewer.Friends[author] != targeted.Friends[author] ||
				viewer.FriendsOfFriends[author] != targeted.FriendsOfFriends[author] || viewer.Blocked[author] != targeted.Blocked[author] {
				t.Fatalf("relationship mismatch viewer=%s author=%s", viewerID, author)
			}
			if viewer.canView(post.Post) != targeted.canView(actual.Post) {
				t.Fatalf("visibility mismatch viewer=%s post=%s", viewerID, post.ID)
			}
		}
		var followeeIDs []string
		if err := db.Select(&followeeIDs, `SELECT followee_id FROM follows WHERE follower_id=$1::text`, viewerID); err != nil {
			t.Fatal(err)
		}
		following := map[string]bool{}
		for _, id := range followeeIDs {
			following[id] = true
		}
		for _, feed := range []string{"public", "friends", "friends-of-friends", "following"} {
			expected := make([]string, 0)
			for _, post := range posts {
				if post.AuthorID != viewerID && post.WindowEndedAt == nil && viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, feed) {
					expected = append(expected, post.ID)
				}
			}
			path := "/api/feeds/" + feed
			offset := 0
			for {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-Test-Viewer", viewerID)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
				}
				var response getFeedResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				actual := make([]string, 0, len(response.Items))
				for _, post := range response.Items {
					actual = append(actual, post.ID)
				}
				end := min(offset+50, len(expected))
				if !reflect.DeepEqual(actual, expected[offset:end]) || response.HasMore != (end < len(expected)) || response.Items == nil {
					t.Fatalf("viewer=%s feed=%s offset=%d got=%v more=%v want=%v", viewerID, feed, offset, actual, response.HasMore, expected[offset:end])
				}
				if !response.HasMore {
					break
				}
				last := response.Items[len(response.Items)-1]
				path = "/api/feeds/" + feed + "?" + url.Values{"beforePostedAt": {last.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {last.ID}}.Encode()
				offset = end
			}
		}
	}
}

// An in-flight post must not block another user's post in the same window,
// but a window transition must still wait for that post to commit.
func TestPostingWindowConcurrentReaders(t *testing.T) {
	db := optimizedTestDB(t)
	if _, err := db.Exec(`
  INSERT INTO users(id,username,display_name,secret_key_hash,region)
   VALUES('author','author','Author',repeat('ab',32),'asia');
  INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline)
   VALUES('w','asia','2026-09-24',clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 day');
 `); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT id FROM posting_windows WHERE id='w' FOR SHARE`); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range map[string]string{"caption": "parallel", "visibility": "public", "location_kind": "none"} {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	image, err := writer.CreateFormFile("image", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := image.Write([]byte("unchanged image bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.POST("/api/posts", createPostHandler(&HandlerContext{DB: db}), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error { c.Set("session", &Session{UserID: "author"}); return next(c) }
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/posts", &body).WithContext(ctx)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("concurrent post: %d %s", rec.Code, rec.Body.String())
	}

	closing, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer closing.Rollback()
	if _, err := closing.Exec(`SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = closing.Exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id='w'`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("window close must wait: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id='w'`); err != nil {
		t.Fatal(err)
	}
}
