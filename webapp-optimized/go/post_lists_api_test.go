package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestPostListsIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx-auth", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	addUser := func(id string) {
		exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', $3, 'asia')`, id, id, id)
	}
	for _, id := range []string{"viewer", "a", "b", "c", "d"} {
		addUser(id)
	}
	session, err := CreateLoginSession(db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL), ('old', 'europe', '2026-09-22', '2026-09-22T00:00:00Z', '2026-09-22T00:02:00Z', clock_timestamp()), ('august', 'asia', '2026-08-31', '2026-08-31T00:00:00Z', '2026-08-31T00:02:00Z', clock_timestamp())`)
	addPost := func(id, author, window, visibility string) {
		exec(`INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')`, id+"-image", author, []byte("image"))
		exec(`INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, $3, $4, 'caption', $5, 'none', '2026-09-23T00:01:00Z')`, id, author, window, id+"-image", visibility)
	}
	addPost("a", "a", "w", "friends")
	addPost("b", "b", "w", "friends_of_friends")
	addPost("c", "c", "w", "public")
	addPost("d", "d", "w", "public")
	exec(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, 'a', 'viewer'), ($2, 'a', 'b')`, uuid.NewString(), uuid.NewString())
	exec(`INSERT INTO follows (id, follower_id, followee_id) VALUES ($1, 'viewer', 'c'), ($2, 'viewer', 'b')`, uuid.NewString(), uuid.NewString())
	exec(`INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ($1, 'd', 'viewer')`, uuid.NewString())
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/posts/:postId", getPostHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/feeds/:feed", getFeedHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/memories", getMemoriesHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/memories/:date", getMemoriesByDateHandler(h), BatchSessionMiddleware(h, true))
	request := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+session.Token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s status=%d want=%d body=%s", path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	page := func(path string) getFeedResponse {
		t.Helper()
		rec := request(path, http.StatusOK)
		var response getFeedResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Items == nil {
			t.Fatal("items must be an array")
		}
		return response
	}
	assertIDs := func(path string, expected ...string) {
		t.Helper()
		var ids []string
		for _, item := range page(path).Items {
			ids = append(ids, item.ID)
		}
		if !reflect.DeepEqual(ids, expected) {
			t.Fatalf("%s ids=%v want=%v", path, ids, expected)
		}
	}
	assertIDs("/api/feeds/public")
	request("/api/posts/c", http.StatusNotFound)
	addPost("own", "viewer", "w", "public")
	assertIDs("/api/feeds/friends", "a")
	assertIDs("/api/feeds/friends-of-friends", "b")
	assertIDs("/api/feeds/following", "c", "b")
	assertIDs("/api/feeds/public", "c")
	for _, id := range []string{"own", "a", "b", "c"} {
		request("/api/posts/"+id, http.StatusOK)
	}
	request("/api/posts/d", http.StatusNotFound)
	request("/api/posts/missing", http.StatusNotFound)
	request("/api/feeds/unknown", http.StatusBadRequest)
	request("/api/feeds/public?beforePostedAt=invalid&beforeId=x", http.StatusBadRequest)
	addPost("old-own", "viewer", "old", "friends")
	addPost("august-own", "viewer", "august", "public")
	addPost("old-other", "a", "old", "public")
	request("/api/posts/old-own", http.StatusOK)
	request("/api/posts/old-other", http.StatusNotFound)
	assertIDs("/api/memories?month=2026-09", "own", "old-own")
	assertIDs("/api/memories/2026-09-22", "old-own")
	assertIDs("/api/memories?month=2026-08", "august-own")
	assertIDs("/api/memories/2026-09-21")
	request("/api/memories?month=invalid", http.StatusBadRequest)
	request("/api/memories/2026-02-30", http.StatusBadRequest)
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='old-own'`)
	assertIDs("/api/memories/2026-09-22")
	request("/api/posts/old-own", http.StatusNotFound)
	for i := range 55 {
		id := fmt.Sprintf("public-%02d", i)
		addUser(id)
		addPost(id, id, "w", "public")
	}
	// Newer invisible candidates must not consume page slots.
	for i := range 55 {
		id := fmt.Sprintf("private-%02d", i)
		addUser(id)
		addPost(id, id, "w", "friends")
		exec(`UPDATE posts SET posted_at='2026-09-23T00:01:30Z' WHERE id=$1`, id)
	}
	first := page("/api/feeds/public")
	if len(first.Items) != 50 || !first.HasMore {
		t.Fatalf("first page count=%d hasMore=%v", len(first.Items), first.HasMore)
	}
	last := first.Items[len(first.Items)-1]
	before := url.Values{"beforePostedAt": {last.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {last.ID}}.Encode()
	second := page("/api/feeds/public?" + before)
	if len(second.Items) != 6 || second.HasMore {
		t.Fatalf("second page count=%d hasMore=%v", len(second.Items), second.HasMore)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatalf("duplicate post %s", item.ID)
		}
		seen[item.ID] = true
	}
	if !seen["c"] || !seen["public-00"] || !seen["public-54"] {
		t.Fatal("missing posts")
	}
	request("/api/feeds/public?beforeId=x", http.StatusBadRequest)
	request("/api/feeds/public?beforePostedAt=2026-09-23T00:00:00Z", http.StatusBadRequest)
	request("/api/memories?month=2026-09&beforeId=x", http.StatusBadRequest)
	assertIDs("/api/memories?month=2026-09&beforePostedAt=2026-09-23T00:01:00Z&beforeId=own")
	assertIDs("/api/memories/2026-09-23?beforePostedAt=2026-09-23T00:01:00Z&beforeId=own")
}
