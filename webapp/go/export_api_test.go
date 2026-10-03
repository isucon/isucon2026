package isureal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestExportPostsIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	tokens := map[string]string{}
	for _, id := range []string{"owner", "other", "empty", "foreign"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	exec(`UPDATE users SET region='europe' WHERE id='foreign'`)
	for i := 0; i < 56; i++ {
		id := fmt.Sprintf("post-%02d", i)
		exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline,ended_at) VALUES($1,'asia','2026-01-01'::date + $2::int,'2026-01-01','2026-01-01','2026-01-02')`, id, i)
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,'owner',$2,'image/jpeg')`, id, []byte("image-"+id))
		exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,latitude,longitude,posted_at) VALUES($1,'owner',$1,$1,'日本語の投稿','friends','exact',35,139,'2026-01-01'::timestamptz + $2::int * interval '1 day')`, id, i)
	}
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='post-55'`)
	exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES('other','other','secret','image/jpeg')`)
	exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES('other','other','post-00','other','public','none',clock_timestamp())`)
	h := &HandlerContext{DB: db, Config: &Config{Region: "asia"}}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.GET("/api/me/export", exportPostsHandler(h), SessionMiddleware(h, true), RegionMiddleware(h))
	request := func(actor string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/me/export?userId=other", nil)
		if actor != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[actor])
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s: %d %s", actor, rec.Code, rec.Body.String())
		}
		return rec
	}
	request("", http.StatusUnauthorized)
	request("foreign", http.StatusForbidden)
	for _, actor := range []string{"owner", "empty"} {
		rec := request(actor, http.StatusOK)
		if rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Disposition") != `attachment; filename="isureal-posts.zip"` {
			t.Fatal(rec.Header())
		}
		archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, file := range archive.File {
			reader, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[file.Name] = data
		}
		var posts []exportPostResponse
		if err := json.Unmarshal(files["posts.json"], &posts); err != nil {
			t.Fatal(err)
		}
		count := 55
		if actor == "empty" {
			count = 0
		}
		if posts == nil || len(posts) != count || len(files) != count+1 {
			t.Fatalf("posts=%d files=%d", len(posts), len(files))
		}
		for i, post := range posts {
			id := fmt.Sprintf("post-%02d", i)
			if post.ID != id || post.Author.ID != actor || post.Caption != "日本語の投稿" || post.Visibility != "friends" || post.Location.Latitude == nil || *post.Location.Latitude != 35 || post.Location.Longitude == nil || *post.Location.Longitude != 139 {
				t.Fatalf("unexpected post: %+v", post)
			}
			if string(files[post.ImagePath]) != "image-"+id {
				t.Fatalf("wrong image for %s", id)
			}
		}
	}
}
