package isureal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestCertifyUserIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
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
	exec(`INSERT INTO administrators(id,username,password_hash) VALUES ('admin','admin','hash')`)
	admin, err := CreateAdminSession(db, "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"target", "viewer", "deleted"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'hash','asia')`, id, id, id)
	}
	exec(`UPDATE users SET status='deleted' WHERE id='deleted'`)
	viewer, err := CreateLoginSession(db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.PUT("/api/admin/users/:userId/certification", certifyUserHandler(h), AdminSessionMiddleware(h, true))
	e.DELETE("/api/admin/users/:userId/certification", deleteUserCertificationHandler(h), AdminSessionMiddleware(h, true))
	e.PUT("/api/follows/:userId", followHandler(h), SessionMiddleware(h, true))
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
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
	path := "/api/admin/users/target/certification"
	for _, token := range []string{"", viewer.Token} {
		check(request(http.MethodPut, path, token), http.StatusUnauthorized)
		check(request(http.MethodDelete, path, token), http.StatusUnauthorized)
	}
	check(request(http.MethodPut, "/api/follows/target", viewer.Token), http.StatusConflict)
	for _, id := range []string{"missing", "deleted"} {
		check(request(http.MethodPut, "/api/admin/users/"+id+"/certification", admin.Token), http.StatusConflict)
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Go(func() { results <- request(http.MethodPut, path, admin.Token) })
	}
	wg.Wait()
	close(results)
	successes := 0
	for rec := range results {
		if rec.Code == http.StatusNoContent {
			successes++
		} else {
			check(rec, http.StatusConflict)
		}
	}
	if successes != 1 {
		t.Fatalf("successful certifications=%d", successes)
	}
	check(request(http.MethodPut, path, admin.Token), http.StatusConflict)
	check(request(http.MethodPut, "/api/follows/target", viewer.Token), http.StatusNoContent)
	check(request(http.MethodDelete, path, admin.Token), http.StatusConflict)
	var accountType string
	if err := db.Get(&accountType, `SELECT account_type FROM users WHERE id='target'`); err != nil {
		t.Fatal(err)
	}
	if accountType != "certified_public" {
		t.Fatalf("account_type=%s", accountType)
	}
	if err := InvalidateAdminSession(db, admin.AdministratorID, admin.TokenHash); err != nil {
		t.Fatal(err)
	}
	check(request(http.MethodPut, path, admin.Token), http.StatusUnauthorized)
}
