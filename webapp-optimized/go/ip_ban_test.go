package isureal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestIPBanIntegration(t *testing.T) {
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
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	config := &Config{Region: "asia", DBHost: u.Hostname(), DBPort: u.Port(), DBUser: u.User.Username(), DBPassword: password, DBName: strings.TrimPrefix(u.Path, "/")}
	e := NewServer(config)

	ban := func(ip string, until string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO ip_bans (ip, banned_until) VALUES ($1, clock_timestamp() + $2::interval)`, ip, until); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path, remoteAddr, clientIP string, status int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"username":"nobody","secretKey":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		if clientIP != "" {
			req.Header.Set("X-Client-IP", clientIP)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s from %s (X-Client-IP %s): %d want=%d %s", method, path, remoteAddr, clientIP, rec.Code, status, rec.Body.String())
		}
		if status == http.StatusForbidden && strings.TrimSpace(rec.Body.String()) != `{"error":"ip_banned"}` {
			t.Fatalf("banned body: %s", rec.Body.String())
		}
	}
	const proxy = "127.0.0.1:40000"

	ban("203.0.113.7", "2 minutes")
	ban("203.0.113.8", "-1 second")
	// The list was loaded when the server started; bans added later apply after
	// the next background reload.
	time.Sleep(ipBanReloadInterval + 50*time.Millisecond)
	request(http.MethodPost, "/api/auth/login", proxy, "203.0.113.7", http.StatusForbidden)
	request(http.MethodGet, "/api/images/missing", proxy, "203.0.113.7", http.StatusForbidden)
	request(http.MethodPost, "/api/auth/login", proxy, "203.0.113.8", http.StatusUnauthorized)
	request(http.MethodPost, "/api/auth/login", proxy, "198.51.100.1", http.StatusUnauthorized)
	request(http.MethodPost, "/api/auth/login", "203.0.113.7:40000", "", http.StatusForbidden)
	request(http.MethodPost, "/api/auth/login", "203.0.113.7:40000", "198.51.100.1", http.StatusUnauthorized)

	// 追加した BAN は再読み込みの間隔のうちに反映され、期限を過ぎたら再読み込みを待たずに解ける。
	ban("198.51.100.1", "600 milliseconds")
	time.Sleep(ipBanReloadInterval + 50*time.Millisecond)
	request(http.MethodPost, "/api/auth/login", proxy, "198.51.100.1", http.StatusForbidden)
	time.Sleep(600 * time.Millisecond)
	request(http.MethodPost, "/api/auth/login", proxy, "198.51.100.1", http.StatusUnauthorized)

	// Requests between the regional apps are not subject to bans: without the
	// sync token this is the sync handler's own 403, not ip_banned.
	req := httptest.NewRequest(http.MethodPost, "/internal/state-reload", nil)
	req.RemoteAddr = "203.0.113.7:40000"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Body.Len() != 0 {
		t.Fatalf("internal request from a banned address: %d %s", rec.Code, rec.Body.String())
	}
}
