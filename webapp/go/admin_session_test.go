package isureal

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestAdminSessionIntegration(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', 'hash'), ('admin2', 'other', 'hash')`); err != nil {
		t.Fatal(err)
	}
	first, err := CreateAdminSession(db, "admin1")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(first.Token))
	if first.Token == "" || first.TokenHash != hex.EncodeToString(hash[:]) || first.AdministratorID != "admin1" || first.CreatedAt.IsZero() {
		t.Fatalf("invalid session: %+v", first)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := CreateAdminSession(db, "admin1"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var active int
	if err := db.Get(&active, `SELECT count(*) FROM administrator_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if active != 9 {
		t.Fatalf("active=%d want=9", active)
	}
	if _, err := CreateAdminSession(db, "missing"); err == nil {
		t.Fatal("missing administrator accepted")
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/admin", func(c *echo.Context) error {
		session := GetAdminSession(c)
		if session == nil || session.AdministratorID != "admin1" {
			t.Error("missing admin session in context")
		}
		return c.NoContent(http.StatusNoContent)
	}, AdminSessionMiddleware(h, true))
	e.GET("/user", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }, SessionMiddleware(h, true))
	check := func(path, header string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s status=%d want=%d body=%s", path, rec.Code, status, rec.Body.String())
		}
	}
	check("/admin", "Bearer "+first.Token, http.StatusNoContent)
	check("/admin", "", http.StatusUnauthorized)
	check("/admin", "Basic invalid", http.StatusUnauthorized)
	check("/admin", "Bearer unknown", http.StatusUnauthorized)
	check("/user", "Bearer "+first.Token, http.StatusUnauthorized)
	if _, err := db.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ('user1', 'alice', 'hash', 'Alice', 'asia')`); err != nil {
		t.Fatal(err)
	}
	userSession, err := CreateLoginSession(db, "user1")
	if err != nil {
		t.Fatal(err)
	}
	check("/admin", "Bearer "+userSession.Token, http.StatusUnauthorized)
	if err := InvalidateAdminSession(db, "admin2", first.TokenHash); err != nil {
		t.Fatal(err)
	}
	check("/admin", "Bearer "+first.Token, http.StatusNoContent)
	for range 2 {
		if err := InvalidateAdminSession(db, "admin1", first.TokenHash); err != nil {
			t.Fatal(err)
		}
	}
	check("/admin", "Bearer "+first.Token, http.StatusUnauthorized)
	if err := db.Get(&active, `SELECT count(*) FROM administrator_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if active != 8 {
		t.Fatalf("logout invalidated other sessions: active=%d", active)
	}
	var invalidated bool
	if err := db.Get(&invalidated, `SELECT invalidated_at IS NOT NULL FROM administrator_sessions WHERE token_hash=$1`, first.TokenHash); err != nil {
		t.Fatal(err)
	}
	if !invalidated {
		t.Fatal("missing invalidated_at")
	}
}
