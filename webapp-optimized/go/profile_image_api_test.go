package isureal

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestProfileImageIntegration(t *testing.T) {
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
	for _, id := range []string{"owner", "viewer"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.PUT("/api/me/profile-image", putProfileImageHandler(h), BatchSessionMiddleware(h, true))
	e.DELETE("/api/me/profile-image", deleteProfileImageHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h))
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	request := func(actor, method, path, media string, body []byte, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", media)
		req.Header.Set("Authorization", "Bearer "+tokens[actor])
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec
	}
	current := func() string {
		t.Helper()
		var id string
		if err := db.Get(&id, `SELECT profile_image_id FROM users WHERE id='owner'`); err != nil {
			t.Fatal(err)
		}
		return id
	}
	count := func(want int) {
		t.Helper()
		var n int
		if err := db.Get(&n, `SELECT count(*) FROM image_objects`); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("images=%d want=%d", n, want)
		}
	}
	path := "/api/me/profile-image"
	request("", http.MethodPut, path, "image/jpeg", data.Bytes(), http.StatusUnauthorized)
	request("owner", http.MethodDelete, path, "", nil, http.StatusNotFound)
	request("owner", http.MethodPut, path, "image/png", data.Bytes(), http.StatusUnsupportedMediaType)
	count(0)
	request("owner", http.MethodPut, path, "image/jpeg", data.Bytes(), http.StatusNoContent)
	old := current()
	owner, err := getUser(context.Background(), db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	oldURL := signedImageURL(owner, old)
	count(1)
	for _, actor := range []string{"owner", "viewer"} {
		rec := request(actor, http.MethodGet, oldURL, "", nil, http.StatusOK)
		if rec.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatal("wrong media")
		}
		if !bytes.Equal(rec.Body.Bytes(), data.Bytes()) {
			t.Fatal("downloaded image differs from uploaded JPEG")
		}
		if _, err := jpeg.Decode(rec.Body); err != nil {
			t.Fatal(err)
		}
	}
	request("", http.MethodGet, oldURL, "", nil, http.StatusOK)
	request("owner", http.MethodGet, "/api/images/"+old, "", nil, http.StatusForbidden)
	for _, pair := range [][2]string{{"owner", "viewer"}, {"viewer", "owner"}} {
		exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES('00000000-0000-0000-0000-000000000001',$1,$2)`, pair[0], pair[1])
		request("viewer", http.MethodGet, oldURL, "", nil, http.StatusOK)
		request("owner", http.MethodGet, oldURL, "", nil, http.StatusOK)
		exec(`DELETE FROM block_relations`)
	}
	exec(`CREATE FUNCTION reject_image_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject image deletion'; END $$`)
	exec(`CREATE TRIGGER reject_image_delete BEFORE DELETE ON image_objects FOR EACH ROW EXECUTE FUNCTION reject_image_delete()`)
	request("owner", http.MethodPut, path, "image/jpeg", data.Bytes(), http.StatusInternalServerError)
	if current() != old {
		t.Fatal("replacement failed to roll back")
	}
	count(1)
	request("owner", http.MethodDelete, path, "", nil, http.StatusInternalServerError)
	if current() != old {
		t.Fatal("deletion failed to roll back")
	}
	count(1)
	exec(`DROP TRIGGER reject_image_delete ON image_objects`)
	exec(`DROP FUNCTION reject_image_delete()`)
	request("owner", http.MethodPut, path, "image/jpeg", data.Bytes(), http.StatusNoContent)
	fresh := current()
	freshURL := signedImageURL(owner, fresh)
	if fresh == old {
		t.Fatal("image ID unchanged")
	}
	count(1)
	request("viewer", http.MethodGet, oldURL, "", nil, http.StatusForbidden)
	request("viewer", http.MethodGet, freshURL, "", nil, http.StatusOK)
	exec(`UPDATE users SET status='deleted' WHERE id='owner'`)
	request("viewer", http.MethodGet, freshURL, "", nil, http.StatusForbidden)
	exec(`UPDATE users SET status='active' WHERE id='owner'`)
	request("owner", http.MethodDelete, path, "", nil, http.StatusNoContent)
	count(0)
	var id *string
	if err := db.Get(&id, `SELECT profile_image_id FROM users WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	if id != nil {
		t.Fatal("profile reference retained")
	}
	request("viewer", http.MethodGet, freshURL, "", nil, http.StatusForbidden)
	request("owner", http.MethodDelete, path, "", nil, http.StatusNotFound)
}
