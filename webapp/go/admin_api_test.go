package isureal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestAdminAPIIntegration(t *testing.T) {
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
	hash := sha256.Sum256([]byte("password"))
	if _, err := db.Exec(`INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', $1)`, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/admin/auth/login", adminLoginHandler(h))
	e.POST("/api/admin/auth/logout", adminLogoutHandler(h), AdminSessionMiddleware(h, true))
	request := func(path, body, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
		}
		return rec
	}
	login := func() adminLoginResponse {
		t.Helper()
		rec := request("/api/admin/auth/login", `{"username":"admin","password":"password"}`, "", http.StatusOK)
		var response adminLoginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Token == "" || response.AdministratorID != "admin1" {
			t.Fatalf("invalid response: %+v", response)
		}
		if strings.Contains(rec.Body.String(), "password") {
			t.Fatal("password exposed")
		}
		return response
	}
	for _, body := range []string{
		`{"username":"admin","password":"wrong"}`,
		`{"username":"missing","password":"password"}`,
		`{"username":"Admin","password":"password"}`,
	} {
		rec := request("/api/admin/auth/login", body, "", http.StatusUnauthorized)
		var response APIErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != "invalid_credentials" {
			t.Fatalf("error=%s", response.Error)
		}
	}
	request("/api/admin/auth/login", `{`, "", http.StatusBadRequest)
	first, second := login(), login()
	if first.Token == second.Token {
		t.Fatal("token reused")
	}
	var active int
	if err := db.Get(&active, `SELECT count(*) FROM administrator_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("active=%d want=2", active)
	}
	request("/api/admin/auth/logout", "", "", http.StatusUnauthorized)
	request("/api/admin/auth/logout", "", "unknown", http.StatusUnauthorized)
	request("/api/admin/auth/logout", "", first.Token, http.StatusNoContent)
	request("/api/admin/auth/logout", "", first.Token, http.StatusUnauthorized)
	request("/api/admin/auth/logout", "", second.Token, http.StatusNoContent)
	if _, err := db.Exec(`CREATE FUNCTION reject_admin_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject session'; END $$; CREATE TRIGGER reject_admin_session BEFORE INSERT ON administrator_sessions FOR EACH ROW EXECUTE FUNCTION reject_admin_session()`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP FUNCTION reject_admin_session() CASCADE`)
	request("/api/admin/auth/login", `{"username":"admin","password":"password"}`, "", http.StatusInternalServerError)
}
