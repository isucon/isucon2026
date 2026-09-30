package isureal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestUserAPIIntegration(t *testing.T) {
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
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/auth/register", registerHandler(h))
	e.POST("/api/auth/login", loginHandler(h))
	e.POST("/api/auth/logout", logoutHandler(h), SessionMiddleware(h, true))
	e.GET("/api/me", getMeHandler(h), SessionMiddleware(h, true))
	e.PATCH("/api/me", updateMeHandler(h), SessionMiddleware(h, true))
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "ignored")
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
	decode := func(rec *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	registered := request("POST", "/api/auth/register", "", `{"username":"alice","displayName":"アリス","region":"asia"}`)
	check(registered, http.StatusCreated)
	result := decode(registered)
	secret := result["secretKey"].(string)
	if secret == "" {
		t.Fatal("missing credentials")
	}
	if _, ok := result["token"]; ok {
		t.Fatal("registration returned a session token")
	}
	var sessionCount int
	if err := db.Get(&sessionCount, `SELECT count(*) FROM login_sessions`); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatal("registration created a session")
	}
	firstLogin := request("POST", "/api/auth/login", "", fmt.Sprintf(`{"username":"alice","secretKey":%q}`, secret))
	check(firstLogin, http.StatusOK)
	token := decode(firstLogin)["token"].(string)
	user := result["user"].(map[string]any)
	if user["username"] != "alice" || user["displayName"] != "アリス" {
		t.Fatalf("user=%v", user)
	}
	me := request("GET", "/api/me", token, "")
	check(me, http.StatusOK)
	meBody := decode(me)
	if _, ok := meBody["profileImageUrl"]; !ok || meBody["profileImageUrl"] != nil {
		t.Fatalf("profileImageUrl=%v", meBody)
	}
	if bytes.Contains(me.Body.Bytes(), []byte("secret")) {
		t.Fatal("credential leaked")
	}
	check(request("POST", "/api/auth/register", "", `{"username":"alice","displayName":"Other","region":"asia"}`), http.StatusConflict)
	check(request("POST", "/api/auth/register", "", `{"username":"ALICE","displayName":"Other","region":"asia"}`), http.StatusBadRequest)
	check(request("POST", "/api/auth/login", "", fmt.Sprintf(`{"username":"ALICE","secretKey":%q}`, secret)), http.StatusUnauthorized)
	unauthorized := request("GET", "/api/me", "", "")
	check(unauthorized, http.StatusUnauthorized)
	if decode(unauthorized)["error"] != "invalid_session" || !strings.HasPrefix(unauthorized.Header().Get("Content-Type"), "application/json") {
		t.Fatal("incompatible error response")
	}
	check(request("POST", "/api/auth/login", "", `{"username":"alice","secretKey":"wrong"}`), http.StatusUnauthorized)
	check(request("POST", "/api/auth/login", "", `{"username":"missing","secretKey":"wrong"}`), http.StatusUnauthorized)
	login := request("POST", "/api/auth/login", "", fmt.Sprintf(`{"username":"alice","secretKey":%q}`, secret))
	check(login, http.StatusOK)
	loginBody := decode(login)
	if _, ok := loginBody["secretKey"]; ok {
		t.Fatal("login returned secretKey")
	}
	newToken := loginBody["token"].(string)
	check(request("GET", "/api/me", token, ""), http.StatusUnauthorized)
	check(request("GET", "/api/me", newToken, ""), http.StatusOK)
	if err := InvalidateLoginSession(db, user["id"].(string), tokenHashForTest(token)); err != nil {
		t.Fatal(err)
	}
	check(request("GET", "/api/me", newToken, ""), http.StatusOK)
	if _, err := db.Exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ('00000000-0000-0000-0000-000000000001', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z')`); err != nil {
		t.Fatal(err)
	}
	patched := request("PATCH", "/api/me", newToken, `{"displayName":"新しい名前","bio":"こんにちは","region":"europe"}`)
	check(patched, http.StatusOK)
	profile := decode(patched)
	if profile["region"] != "europe" || profile["bio"] != "こんにちは" {
		t.Fatalf("profile=%v", profile)
	}
	check(request("PATCH", "/api/me", newToken, `{"bio":""}`), http.StatusOK)
	meBody = decode(request("GET", "/api/me", newToken, ""))
	if meBody["displayName"] != "新しい名前" || meBody["bio"] != "" || meBody["region"] != "europe" {
		t.Fatalf("partial update=%v", meBody)
	}
	for _, body := range []string{`{}`, `{"displayName":""}`, `{"region":"invalid"}`, `{"username":"changed"}`, `null`, `{} {}`} {
		check(request("PATCH", "/api/me", newToken, body), http.StatusBadRequest)
	}
	check(request("PATCH", "/api/me", newToken, fmt.Sprintf(`{"displayName":%q}`, strings.Repeat("あ", 100))), http.StatusOK)
	check(request("PATCH", "/api/me", newToken, fmt.Sprintf(`{"displayName":%q}`, strings.Repeat("あ", 101))), http.StatusBadRequest)
	check(request("POST", "/api/auth/logout", newToken, ""), http.StatusNoContent)
	check(request("GET", "/api/me", newToken, ""), http.StatusUnauthorized)
	check(request("POST", "/api/auth/logout", newToken, ""), http.StatusUnauthorized)
	check(request("GET", "/api/v1/me", newToken, ""), http.StatusNotFound)
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- request("POST", "/api/auth/register", "", `{"username":"concurrent","displayName":"同時登録","region":"asia"}`).Code
		}()
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == http.StatusCreated {
			created++
		} else if status != http.StatusConflict {
			t.Errorf("concurrent registration status=%d", status)
		}
	}
	if created != 1 {
		t.Fatalf("concurrent registrations=%d", created)
	}
	if _, err := db.Exec(`CREATE OR REPLACE FUNCTION reject_user_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'insertion rejected'; END $$; CREATE TRIGGER reject_user_session BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_user_session()`); err != nil {
		t.Fatal(err)
	}
	// スキーマを作り直しても関数は残るので、次回のテストのために消す。
	defer db.Exec(`DROP FUNCTION reject_user_session() CASCADE`)
	independent := request("POST", "/api/auth/register", "", `{"username":"independent","displayName":"Independent","region":"asia"}`)
	check(independent, http.StatusCreated)
	independentSecret := decode(independent)["secretKey"].(string)
	check(request("POST", "/api/auth/login", "", fmt.Sprintf(`{"username":"independent","secretKey":%q}`, independentSecret)), http.StatusInternalServerError)
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM login_sessions s JOIN users u ON u.id=s.user_id WHERE u.username='independent'`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed login left a session behind")
	}

}

func tokenHashForTest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
