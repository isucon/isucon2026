package isureal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestRegionIntegration(t *testing.T) {
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
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	config := &Config{Region: "asia", DBHost: u.Hostname(), DBPort: u.Port(), DBUser: u.User.Username(), DBPassword: password, DBName: strings.TrimPrefix(u.Path, "/")}
	e := NewServer(config)
	request := func(method, path, token, body, media string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", media)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d want=%d %s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	rec := request(http.MethodPost, "/api/auth/register", "", `{"username":"traveler","displayName":"Traveler","region":"europe"}`, "application/json", http.StatusCreated)
	var registration registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(loginRequest{Username: "traveler", SecretKey: registration.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	login := func() string {
		t.Helper()
		rec := request(http.MethodPost, "/api/auth/login", "", string(body), "application/json", http.StatusOK)
		var out loginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}
	token := login()
	routes := [][2]string{
		{"GET", "/api/me"}, {"GET", "/api/users?q=x"}, {"GET", "/api/users/other"},
		{"POST", "/api/me/account-deletion"}, {"POST", "/api/posts"}, {"GET", "/api/posting-window/current"}, {"GET", "/api/posts/me/current"},
		{"GET", "/api/posts/post"}, {"PATCH", "/api/posts/post"}, {"DELETE", "/api/posts/post"}, {"GET", "/api/feeds/public"},
		{"GET", "/api/memories?month=2026-09"}, {"GET", "/api/memories/2026-09-24"},
		{"POST", "/api/posts/post/comments"}, {"GET", "/api/posts/post/comments"}, {"DELETE", "/api/comments/comment"},
		{"POST", "/api/posts/post/realmojis"}, {"GET", "/api/posts/post/realmojis"}, {"DELETE", "/api/realmojis/reaction"},
		{"GET", "/api/notifications"}, {"POST", "/api/friend-requests"},
		{"POST", "/api/friend-requests/request/accept"}, {"GET", "/api/relationships/friends"}, {"DELETE", "/api/friendships/other"},
		{"PUT", "/api/follows/other"}, {"DELETE", "/api/follows/other"}, {"PUT", "/api/blocks/other"}, {"DELETE", "/api/blocks/other"},
	}
	for _, route := range routes {
		rec := request(route[0], route[1], token, "", "application/json", http.StatusForbidden)
		var out APIErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Error != "region_mismatch" {
			t.Fatalf("unexpected error %s", out.Error)
		}
	}
	request("GET", "/api/me", "", "", "", http.StatusUnauthorized)
	request("PATCH", "/api/me", token, `{"displayName":"Changed","bio":"bio"}`, "application/json", http.StatusOK)
	request("PUT", "/api/me/profile-image", token, string(testJPEG(t)), "image/jpeg", http.StatusNoContent)
	request("DELETE", "/api/me/profile-image", token, "", "", http.StatusNoContent)
	request("PATCH", "/api/me", token, `{"region":"asia"}`, "application/json", http.StatusOK)
	request("GET", "/api/me", token, "", "", http.StatusOK)
	request("GET", "/api/memories?month=2026-09", token, "", "", http.StatusOK)
	request("PATCH", "/api/me", token, `{"region":"america"}`, "application/json", http.StatusOK)
	request("GET", "/api/me", token, "", "", http.StatusForbidden)
	request("POST", "/api/auth/logout", token, "", "", http.StatusNoContent)
	request("GET", "/api/me", token, "", "", http.StatusUnauthorized)
	token = login()
	config.Region = "america"
	request("GET", "/api/me", token, "", "", http.StatusOK)
	config.Region = "europe"
	request("GET", "/api/me", token, "", "", http.StatusForbidden)
	if _, err := db.Exec(`INSERT INTO administrators(id,username,password_hash) VALUES('admin','admin','hash')`); err != nil {
		t.Fatal(err)
	}
	admin, err := CreateAdminSession(db, "admin")
	if err != nil {
		t.Fatal(err)
	}
	request("PUT", "/api/admin/users/"+registration.User.ID+"/certification", admin.Token, "", "", http.StatusNoContent)
}

func TestRegionConfig(t *testing.T) {
	t.Setenv("ISUREAL_REGION", "")
	if ConfigFromEnv().Region != "asia" {
		t.Fatal("default region")
	}
	t.Setenv("ISUREAL_REGION", "europe")
	if ConfigFromEnv().Region != "europe" {
		t.Fatal("configured region")
	}
}
