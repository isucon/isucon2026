package isureal

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestCreatePostIntegration(t *testing.T) {
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
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	userID, windowID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'alice', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'アリス', 'asia')`, userID)
	session, err := CreateLoginSession(db, userID)
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/posts", createPostHandler(h), BatchSessionMiddleware(h, true))
	e.PATCH("/api/posts/:postId", updatePostHandler(h), BatchSessionMiddleware(h, true))
	e.DELETE("/api/posts/:postId", deletePostHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/posts/:postId", getPostHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h))
	action := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	makeBody := func(data []byte, location map[string]string) ([]byte, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"caption": "こんにちは", "visibility": "public"} {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		for key, value := range location {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("image", "image.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return body.Bytes(), writer.FormDataContentType()
	}
	body, contentType := makeBody(jpegData.Bytes(), map[string]string{"location_kind": "exact", "latitude": "35", "longitude": "139"})
	request := func(token string, body []byte, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
		}
	}
	check(request("", body, contentType), http.StatusUnauthorized)
	check(request(session.Token, body, contentType), http.StatusConflict)
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-23', clock_timestamp() - interval '1 minute', clock_timestamp() + interval '1 minute')`, windowID)
	badBody, badType := makeBody(jpegData.Bytes(), map[string]string{"location_kind": "exact", "latitude": "91", "longitude": "139"})
	check(request(session.Token, badBody, badType), http.StatusBadRequest)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for range 8 {
		wg.Go(func() { results <- request(session.Token, body, contentType) })
	}
	wg.Wait()
	close(results)
	successes := 0
	var created createPostResponse
	for rec := range results {
		if rec.Code == http.StatusCreated {
			successes++
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
		} else {
			check(rec, http.StatusConflict)
		}
	}
	if successes != 1 {
		t.Fatalf("created=%d", successes)
	}
	if created.Author.ID != userID || created.PostingWindowID != windowID || !created.OnTime || created.LateSeconds != 0 || created.Location.Latitude == nil || *created.Location.Latitude != 35 {
		t.Fatalf("unexpected response: %+v", created)
	}
	var stored []byte
	if err := db.Get(&stored, `SELECT data FROM image_objects WHERE owner_user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, jpegData.Bytes()) {
		t.Fatal("stored image differs from uploaded JPEG")
	}
	downloaded := action(http.MethodGet, created.ImageURL, session.Token, "")
	check(downloaded, http.StatusOK)
	if !bytes.Equal(downloaded.Body.Bytes(), jpegData.Bytes()) {
		t.Fatal("downloaded image differs from uploaded JPEG")
	}
	path := "/api/posts/" + created.ID
	check(action(http.MethodPatch, path, "", `{"caption":"edited"}`), http.StatusUnauthorized)
	check(action(http.MethodDelete, path, "", ""), http.StatusUnauthorized)
	check(action(http.MethodPatch, "/api/posts/missing", session.Token, `{"caption":"edited"}`), http.StatusNotFound)
	check(action(http.MethodDelete, "/api/posts/missing", session.Token, ""), http.StatusNotFound)
	for _, invalid := range []string{`{`, `{}`, `{"visibility":"invalid"}`} {
		check(action(http.MethodPatch, path, session.Token, invalid), http.StatusBadRequest)
	}
	exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ('other', 'other', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Other', 'asia')`)
	other, err := CreateLoginSession(db, "other")
	if err != nil {
		t.Fatal(err)
	}
	check(action(http.MethodPatch, path, other.Token, `{"caption":"edited"}`), http.StatusForbidden)
	check(action(http.MethodDelete, path, other.Token, ""), http.StatusForbidden)
	updated := action(http.MethodPatch, path, session.Token, `{"caption":"edited"}`)
	check(updated, http.StatusOK)
	var edited updatePostResponse
	if err := json.Unmarshal(updated.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.Caption != "edited" || edited.Visibility != created.Visibility || strings.Split(edited.ImageURL, "?")[0] != strings.Split(created.ImageURL, "?")[0] || !edited.PostedAt.Equal(created.PostedAt) {
		t.Fatalf("unexpected edit: %+v", edited)
	}
	check(action(http.MethodPatch, path, session.Token, `{"visibility":"friends"}`), http.StatusOK)
	exec(`CREATE FUNCTION reject_visibility() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject visibility'; END $$`)
	exec(`CREATE TRIGGER reject_visibility BEFORE UPDATE OF visibility ON posts FOR EACH ROW EXECUTE FUNCTION reject_visibility()`)
	check(action(http.MethodPatch, path, session.Token, `{"caption":"rolled back","visibility":"public"}`), http.StatusInternalServerError)
	exec(`DROP FUNCTION reject_visibility() CASCADE`)
	var caption string
	if err := db.Get(&caption, `SELECT caption FROM posts WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if caption != "edited" {
		t.Fatal("partial update was not rolled back")
	}
	check(action(http.MethodPatch, path, session.Token, `{"caption":""}`), http.StatusOK)
	europeWindowID := uuid.NewString()
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'europe', '2026-09-23', clock_timestamp()-interval '1 minute', clock_timestamp()-interval '10 seconds')`, europeWindowID)
	exec(`UPDATE users SET region='europe' WHERE id=$1`, userID)
	check(request(session.Token, body, contentType), http.StatusConflict)
	deletes := make(chan *httptest.ResponseRecorder, 8)
	for range 8 {
		wg.Go(func() { deletes <- action(http.MethodDelete, path, session.Token, "") })
	}
	wg.Wait()
	close(deletes)
	deleted := 0
	for rec := range deletes {
		if rec.Code == http.StatusNoContent {
			deleted++
		} else {
			check(rec, http.StatusNotFound)
		}
	}
	if deleted != 1 {
		t.Fatalf("successful deletes=%d", deleted)
	}
	check(action(http.MethodGet, path, session.Token, ""), http.StatusNotFound)
	check(action(http.MethodGet, created.ImageURL, "", ""), http.StatusForbidden)
	check(action(http.MethodPatch, path, session.Token, `{"caption":"edited"}`), http.StatusNotFound)
	exec(`UPDATE posting_windows SET on_time_deadline=clock_timestamp()-interval '10 seconds' WHERE id=$1`, windowID)
	rec := request(session.Token, body, contentType)
	check(rec, http.StatusCreated)
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OnTime || created.LateSeconds < 10 {
		t.Fatalf("late response: %+v", created)
	}
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id=$1`, europeWindowID)
	check(action(http.MethodPatch, "/api/posts/"+created.ID, session.Token, `{"caption":"edited"}`), http.StatusForbidden)
	check(action(http.MethodDelete, "/api/posts/"+created.ID, session.Token, ""), http.StatusNoContent)
	exec(`UPDATE users SET region='asia' WHERE id=$1`, userID)
	check(request(session.Token, body, contentType), http.StatusConflict)
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id=$1`, windowID)
	check(request(session.Token, body, contentType), http.StatusConflict)
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp()+interval '1 minute')`, uuid.NewString())
	exec(`CREATE FUNCTION reject_post() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject post'; END $$`)
	exec(`CREATE TRIGGER reject_post BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION reject_post()`)
	defer db.Exec(`DROP FUNCTION reject_post() CASCADE`)
	check(request(session.Token, body, contentType), http.StatusInternalServerError)
	var imageCount int
	if err := db.Get(&imageCount, `SELECT count(*) FROM image_objects`); err != nil {
		t.Fatal(err)
	}
	if imageCount != 2 {
		t.Fatalf("image insert was not rolled back: count=%d", imageCount)
	}
}
