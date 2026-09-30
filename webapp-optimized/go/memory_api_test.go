package isureal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMemoryInteractionsIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx-auth", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	tokens := map[string]string{}
	for _, id := range []string{"author", "reactor", "stranger"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('window','asia','2026-09-24',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	for _, id := range []string{"author", "reactor"} {
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'photo','image/jpeg')`, id+"-image", id)
		exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES($1,$2,'window',$3,'public','none',clock_timestamp())`, id+"-post", id, id+"-image")
	}
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp()`)
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/memories", getMemoriesHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/memories/:date", getMemoriesByDateHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/posts/:postId/comments", getCommentsHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h))
	request := func(actor, path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tokens[actor])
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec
	}
	page := func(actor, path string) getMemoriesResponse {
		t.Helper()
		rec := request(actor, path, http.StatusOK)
		var out getMemoriesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, path := range []string{"/api/memories?month=2026-09", "/api/memories/2026-09-24"} {
		out := page("author", path)
		if len(out.Items) != 1 || out.Items[0].RealMojis == nil || out.Items[0].Comments == nil {
			t.Fatalf("empty arrays: %+v", out)
		}
	}
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("r-%02d", i)
		exec(`INSERT INTO users(id,username,secret_key_hash,display_name,region) VALUES($1,$2,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6',$3,'asia')`, id, id, id)
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,$3,'image/jpeg')`, id+"-image", id, []byte("original bytes"))
		exec(`INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES($1,'author-post',$2,$3,'like','2026-09-24T01:00:00Z')`, id, id, id+"-image")
		exec(`INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES($1,'author-post',$2,'comment','2026-09-24T01:00:00Z')`, id, id)
	}
	exec(`UPDATE realmojis SET replaced_at=clock_timestamp() WHERE id='r-54'`)
	exec(`UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id='r-53'`)
	exec(`UPDATE comments SET deleted_at=clock_timestamp() WHERE id='r-00'`)
	exec(`UPDATE users SET status='deleted',deleted_at=clock_timestamp() WHERE id IN ('r-01','r-52')`)
	for _, path := range []string{"/api/memories?month=2026-09", "/api/memories/2026-09-24"} {
		out := page("author", path)
		if len(out.Items) != 1 || out.HasMore {
			t.Fatalf("memories: %+v", out)
		}
		item := out.Items[0]
		if len(item.RealMojis) != 50 || len(item.Comments) != 50 {
			t.Fatalf("counts: %d %d", len(item.RealMojis), len(item.Comments))
		}
		if item.RealMojis[0].ID != "r-51" || item.RealMojis[49].ID != "r-02" {
			t.Fatal("reaction ordering or filtering")
		}
		if item.Comments[0].ID != "r-02" || item.Comments[49].ID != "r-51" {
			t.Fatal("comment ordering or filtering")
		}
		rec := request("author", item.RealMojis[0].ImageURL, http.StatusOK)
		if !bytes.Equal(rec.Body.Bytes(), []byte("original bytes")) {
			t.Fatal("image bytes changed")
		}
		request("", item.RealMojis[0].ImageURL, http.StatusOK)
	}
	request("author", "/api/posts/author-post/comments", http.StatusForbidden)
	other := page("reactor", "/api/memories?month=2026-09")
	if len(other.Items) != 1 || len(other.Items[0].Comments) != 0 || len(other.Items[0].RealMojis) != 0 {
		t.Fatal("another user's interactions leaked")
	}
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='author-post'`)
	if out := page("author", "/api/memories?month=2026-09"); len(out.Items) != 0 {
		t.Fatal("deleted post listed")
	}
}
