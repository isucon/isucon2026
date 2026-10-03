package isureal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestAccountDeletionIntegration(t *testing.T) {
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
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/auth/register", registerHandler(h))
	e.POST("/api/auth/login", loginHandler(h))
	e.POST("/api/me/account-deletion", deleteAccountHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/me", getMeHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/users", searchUsersHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/users/:userId", getUserHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/posts/:postId", getPostHandler(h), BatchSessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h))
	e.GET("/api/relationships/:kind", getRelationshipsHandler(h), BatchSessionMiddleware(h, true))
	request := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d want=%d body=%s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	regBody := `{"username":"retiring","displayName":"Retiring","region":"asia"}`
	rec := request(http.MethodPost, "/api/auth/register", "", regBody, http.StatusCreated)
	var registration registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	id := registration.User.ID
	loginBody, err := json.Marshal(loginRequest{Username: "retiring", SecretKey: registration.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	login := func() loginResponse {
		t.Helper()
		rec := request(http.MethodPost, "/api/auth/login", "", string(loginBody), http.StatusOK)
		var out loginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	old := login()
	current := login()
	exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES('viewer','viewer','Viewer','9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`)
	viewer, err := CreateLoginSession(db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES('profile',$1,'profile','image/jpeg'),('post-image',$1,'post','image/jpeg'),('viewer-image','viewer','post','image/jpeg')`, id)
	exec(`UPDATE users SET profile_image_id='profile' WHERE id=$1`, id)
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('window','asia','2026-09-24',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES('post',$1,'window','post-image','public','none',clock_timestamp()),('viewer-post','viewer','window','viewer-image','public','none',clock_timestamp())`, id)
	exec(`INSERT INTO friendships(id,user_low_id,user_high_id) VALUES('00000000-0000-0000-0000-000000000001',$1,$2)`, min(id, "viewer"), max(id, "viewer"))
	owner, err := getUser(context.Background(), db, id)
	if err != nil {
		t.Fatal(err)
	}
	profileURL := signedImageURL(owner, "profile")
	postURL := signedImageURL(owner, "post-image")
	path := "/api/me/account-deletion"
	request(http.MethodPost, path, "", "", http.StatusUnauthorized)
	request(http.MethodPost, path, old.Token, "", http.StatusUnauthorized)
	request(http.MethodGet, "/api/posts/post", viewer.Token, "", http.StatusOK)
	request(http.MethodGet, profileURL, "", "", http.StatusOK)
	exec(`CREATE FUNCTION reject_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.reason='account_deletion' THEN RAISE EXCEPTION 'reject retirement'; END IF; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER reject_retirement BEFORE UPDATE ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_retirement()`)
	request(http.MethodPost, path, current.Token, "", http.StatusInternalServerError)
	var state struct {
		Status    string     `db:"status"`
		DeletedAt *time.Time `db:"deleted_at"`
	}
	if err := db.Get(&state, `SELECT status,deleted_at FROM users WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if state.Status != "active" || state.DeletedAt != nil {
		t.Fatalf("rollback state=%+v", state)
	}
	request(http.MethodGet, "/api/me", current.Token, "", http.StatusOK)
	exec(`DROP TRIGGER reject_retirement ON login_sessions`)
	exec(`DROP FUNCTION reject_retirement()`)
	rec = request(http.MethodPost, path, current.Token, "", http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatal("unexpected response body")
	}
	if err := db.Get(&state, `SELECT status,deleted_at FROM users WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if state.Status != "deleted" || state.DeletedAt == nil {
		t.Fatalf("retirement state=%+v", state)
	}
	var active int
	if err := db.Get(&active, `SELECT count(*) FROM login_sessions WHERE user_id=$1 AND active`, id); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active sessions=%d", active)
	}
	var reason string
	if err := db.Get(&reason, `SELECT reason FROM login_sessions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, id); err != nil {
		t.Fatal(err)
	}
	if reason != "account_deletion" {
		t.Fatalf("reason=%s", reason)
	}
	request(http.MethodGet, "/api/me", current.Token, "", http.StatusUnauthorized)
	request(http.MethodPost, path, current.Token, "", http.StatusUnauthorized)
	request(http.MethodPost, "/api/auth/login", "", string(loginBody), http.StatusUnauthorized)
	request(http.MethodPost, "/api/auth/register", "", regBody, http.StatusConflict)
	request(http.MethodGet, "/api/users/"+id, viewer.Token, "", http.StatusNotFound)
	request(http.MethodGet, "/api/posts/post", viewer.Token, "", http.StatusNotFound)
	request(http.MethodGet, postURL, "", "", http.StatusForbidden)
	request(http.MethodGet, profileURL, "", "", http.StatusForbidden)
	rec = request(http.MethodGet, "/api/users?q=retiring", viewer.Token, "", http.StatusOK)
	var search searchUsersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Items) != 0 {
		t.Fatal("retired user searchable")
	}
	rec = request(http.MethodGet, "/api/relationships/friends", viewer.Token, "", http.StatusOK)
	var friends getRelationshipsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &friends); err != nil {
		t.Fatal(err)
	}
	if len(friends.Items) != 0 {
		t.Fatal("retired friend visible")
	}
	var retained int
	if err := db.Get(&retained, `SELECT count(*) FROM posts WHERE author_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if retained != 1 {
		t.Fatal("post removed")
	}
	request(http.MethodGet, "/api/me", viewer.Token, "", http.StatusOK)
}
