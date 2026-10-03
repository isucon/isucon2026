package isureal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestPostReadAPIIntegration(t *testing.T) {
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
	tokens := map[string]string{}
	for _, id := range []string{"a", "b", "c"} {
		exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', $3, 'asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/posting-window/current", getCurrentPostingWindowHandler(h), SessionMiddleware(h, true))
	e.GET("/api/posts/me/current", getMyCurrentPostHandler(h), SessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h))
	e.GET("/api/posts/:postId", getPostHandler(h), SessionMiddleware(h, true))
	request := func(path, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s status=%d want=%d body=%s", path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	checkWindow := func(hasWindow, hasPosted, canPost bool, deletions int) {
		t.Helper()
		rec := request("/api/posting-window/current", tokens["a"], http.StatusOK)
		var response getCurrentPostingWindowResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if (response.Window != nil) != hasWindow || response.HasPosted != hasPosted || response.CanPost != canPost || response.DeletionCount != deletions {
			t.Fatalf("unexpected window: %+v", response)
		}
		if response.Window != nil && response.Window.LocalDate != "2026-09-23" {
			t.Fatalf("invalid date: %+v", response.Window)
		}
	}
	request("/api/posting-window/current", "", http.StatusUnauthorized)
	request("/api/posts/me/current", "", http.StatusUnauthorized)
	request("/api/images/missing", "", http.StatusForbidden)
	checkWindow(false, false, false, 0)
	request("/api/posts/me/current", tokens["a"], http.StatusNotFound)
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ('w', 'asia', '2026-09-23', clock_timestamp()-interval '1 minute', clock_timestamp()+interval '1 minute')`)
	checkWindow(true, false, true, 0)
	addPost := func(id, user string) {
		t.Helper()
		exec(`INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')`, id+"-image", user, []byte("image data"))
		exec(`INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, 'w', $3, 'caption', 'public', 'none', clock_timestamp())`, id, user, id+"-image")
	}
	addPost("pa", "a")
	checkWindow(true, true, false, 0)
	rec := request("/api/posts/me/current", tokens["a"], http.StatusOK)
	var post getMyCurrentPostResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &post); err != nil {
		t.Fatal(err)
	}
	if post.ID != "pa" || post.Author.ID != "a" || !strings.HasPrefix(post.ImageURL, "/api/images/pa-image?") || !post.OnTime {
		t.Fatalf("unexpected post: %+v", post)
	}
	rec = request(post.ImageURL, "", http.StatusOK)
	if rec.Body.String() != "image data" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("invalid image response")
	}
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	request("/api/images/missing", tokens["a"], http.StatusForbidden)
	addPost("pb", "b")
	request("/api/posts/pa", tokens["b"], http.StatusOK)
	exec(`UPDATE posts SET visibility='friends' WHERE id='pa'`)
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	exec(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, 'a', 'b')`, uuid.NewString())
	request("/api/posts/pa", tokens["b"], http.StatusOK)
	exec(`INSERT INTO block_relations (id, blocker_id, blocked_id) VALUES ($1, 'a', 'b')`, uuid.NewString())
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	exec(`UPDATE block_relations SET blocker_id='b', blocked_id='a'`)
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	exec(`DELETE FROM block_relations`)
	exec(`DELETE FROM friendships`)
	exec(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, 'a', 'c'), ($2, 'b', 'c')`, uuid.NewString(), uuid.NewString())
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	exec(`UPDATE posts SET visibility='friends_of_friends' WHERE id='pa'`)
	request("/api/posts/pa", tokens["b"], http.StatusOK)
	exec(`UPDATE users SET region='europe' WHERE id='b'`)
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ('europe', 'europe', '2026-09-23', clock_timestamp()-interval '1 minute', clock_timestamp())`)
	request("/api/posts/pa", tokens["b"], http.StatusOK)
	exec(`UPDATE users SET region='europe' WHERE id='a'`)
	checkWindow(true, true, false, 0)
	request("/api/posts/me/current", tokens["a"], http.StatusOK)
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id='w'`)
	checkWindow(true, true, false, 0)
	request("/api/posts/me/current", tokens["a"], http.StatusOK)
	exec(`UPDATE posting_windows SET local_date='2026-09-24' WHERE id='europe'`)
	request("/api/posts/me/current", tokens["a"], http.StatusNotFound)
	exec(`UPDATE users SET region='asia' WHERE id IN ('a', 'b')`)
	exec(`UPDATE posting_windows SET ended_at=NULL WHERE id='w'`)

	exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id='w'`)
	checkWindow(false, false, false, 0)
	request("/api/posts/me/current", tokens["a"], http.StatusNotFound)
	request("/api/posts/pa", tokens["b"], http.StatusNotFound)
	request(post.ImageURL, tokens["a"], http.StatusOK)
	exec(`UPDATE posting_windows SET ended_at=NULL WHERE id='w'`)
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='pa'`)
	exec(`UPDATE posting_windows SET local_date='2026-09-23' WHERE id='europe'`)
	exec(`UPDATE users SET region='europe' WHERE id='a'`)
	checkWindow(true, false, true, 1)
	request("/api/posts/me/current", tokens["a"], http.StatusNotFound)
	request(post.ImageURL, "", http.StatusForbidden)
	addPost("pa2", "a")
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='pa2'`)
	checkWindow(true, false, false, 2)
}
