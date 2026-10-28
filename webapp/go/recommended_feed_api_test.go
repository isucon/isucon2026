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

func TestRecommendedFeedIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
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
	for _, id := range []string{"viewer", "f1", "f2", "a1", "a2", "x"} {
		exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', $3, 'asia')`, id, id, id)
	}
	session, err := CreateLoginSession(db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)`)
	for i, author := range []string{"viewer", "a1", "a2", "x", "f1"} {
		exec(`INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')`, author+"-image", author, []byte("image"))
		exec(`INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, 'w', $3, 'caption', 'friends_of_friends', 'none', $4)`,
			author, author, author+"-image", time.Date(2026, 9, 23, 0, 1, i, 0, time.UTC))
	}
	for _, pair := range [][2]string{{"f1", "viewer"}, {"f2", "viewer"}, {"a1", "f1"}, {"a1", "f2"}, {"a2", "f1"}, {"a1", "x"}, {"a2", "x"}, {"f2", "x"}} {
		exec(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, $2, $3)`, uuid.NewString(), min(pair[0], pair[1]), max(pair[0], pair[1]))
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/feeds/:feed", getFeedHandler(h), SessionMiddleware(h, true))
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
	assertRanking := func(path string, expected ...string) getFeedResponse {
		t.Helper()
		var response getFeedResponse
		if err := json.Unmarshal(request(path, http.StatusOK).Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, item := range response.Items {
			if item.Score == nil {
				t.Fatalf("%s: post %s has no score", path, item.ID)
			}
			got = append(got, fmt.Sprintf("%s:%d", item.ID, *item.Score))
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("%s ranking=%v want=%v", path, got, expected)
		}
		return response
	}
	const feed = "/api/feeds/friends-of-friends"
	page := assertRanking(feed, "a1:7", "x:6", "a2:4")
	exec(`INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a2', 'f1', 'rm', 'smile', clock_timestamp())`, uuid.NewString())
	exec(`INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES ($1, 'a2', 'f1', 'nice', clock_timestamp()), ($2, 'a2', 'x', 'nice', clock_timestamp())`, uuid.NewString(), uuid.NewString())
	page = assertRanking(feed, "a2:14", "a1:7", "x:6")
	exec(`INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a1', 'f2', 'rm', 'smile', clock_timestamp())`, uuid.NewString())
	exec(`UPDATE users SET status='deleted' WHERE id='f2'`)
	assertRanking(feed, "a2:14", "a1:7", "x:6")

	cursor := page.Items[1]
	before := url.Values{"beforeScore": {fmt.Sprint(*cursor.Score)}, "beforePostedAt": {cursor.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {cursor.ID}}.Encode()
	assertRanking(feed+"?"+before, "x:6")
	request(feed+"?beforeScore=7", http.StatusBadRequest)
	request(feed+"?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1", http.StatusBadRequest)

	var friends getFeedResponse
	if err := json.Unmarshal(request("/api/feeds/friends", http.StatusOK).Body.Bytes(), &friends); err != nil {
		t.Fatal(err)
	}
	if len(friends.Items) != 1 || friends.Items[0].ID != "f1" || friends.Items[0].Score != nil {
		t.Fatalf("friends feed: %+v", friends.Items)
	}
	request("/api/feeds/friends?beforeScore=abc", http.StatusBadRequest)
}
