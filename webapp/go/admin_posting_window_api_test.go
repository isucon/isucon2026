package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestOpenPostingWindowIntegration(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	exec(`INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', 'hash')`)
	exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region, status) VALUES ('a', 'alice', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Alice', 'asia', 'active'), ('b', 'bob', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Bob', 'asia', 'active'), ('c', 'carol', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Carol', 'europe', 'active'), ('d', 'dave', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Dave', 'asia', 'deleted')`)
	session, err := CreateAdminSession(db, "admin1")
	if err != nil {
		t.Fatal(err)
	}
	userSession, err := CreateLoginSession(db, "a")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/admin/posting-window", openPostingWindowHandler(h), AdminSessionMiddleware(h, true))
	request := func(region, body, token string) *httptest.ResponseRecorder {
		body = strings.Replace(body, "{", fmt.Sprintf(`{"region":%q,`, region), 1)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/posting-window", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
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
	body := `{"localDate":"2026-09-23","onTimeSeconds":5}`
	check(request("asia", body, "unknown"), http.StatusUnauthorized)
	check(request("asia", body, userSession.Token), http.StatusUnauthorized)
	check(request("unknown", body, session.Token), http.StatusBadRequest)
	check(request("", body, session.Token), http.StatusBadRequest)
	for _, invalid := range []string{`{`, `{"localDate":"invalid"}`} {
		check(request("asia", invalid, session.Token), http.StatusBadRequest)
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for range 8 {
		wg.Go(func() { results <- request("asia", body, session.Token) })
	}
	wg.Wait()
	close(results)
	successes := 0
	var first openPostingWindowResponse
	for rec := range results {
		if rec.Code == http.StatusCreated {
			successes++
			if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
				t.Fatal(err)
			}
		} else {
			check(rec, http.StatusConflict)
		}
	}
	if successes != 1 || first.Region != "asia" || first.OnTimeDeadline.Sub(first.StartsAt) != 5*time.Second {
		t.Fatalf("successes=%d response=%+v", successes, first)
	}
	var recipients []string
	if err := db.Select(&recipients, `SELECT user_id FROM notifications WHERE posting_window_id=$1 ORDER BY user_id`, first.ID); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(recipients) != "[a b]" {
		t.Fatalf("recipients=%v", recipients)
	}
	check(request("asia", `{"localDate":"2026-09-22"}`, session.Token), http.StatusConflict)
	rec := request("europe", `{"localDate":"2026-09-23"}`, session.Token)
	check(rec, http.StatusCreated)
	var europe openPostingWindowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &europe); err != nil {
		t.Fatal(err)
	}
	if europe.OnTimeDeadline.Sub(europe.StartsAt) != 24*time.Hour {
		t.Fatal("invalid default deadline")
	}
	rec = request("asia", `{"localDate":"2026-09-24"}`, session.Token)
	check(rec, http.StatusCreated)
	var second openPostingWindowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	var endedAt time.Time
	if err := db.Get(&endedAt, `SELECT ended_at FROM posting_windows WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if !endedAt.Equal(second.StartsAt) {
		t.Fatal("previous window ended at different time")
	}
	exec(`CREATE FUNCTION reject_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject notification'; END $$`)
	exec(`CREATE TRIGGER reject_notification BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION reject_notification()`)
	defer db.Exec(`DROP FUNCTION reject_notification() CASCADE`)
	check(request("asia", `{"localDate":"2026-09-25"}`, session.Token), http.StatusInternalServerError)
	var activeIDs []string
	if err := db.Select(&activeIDs, `SELECT id FROM posting_windows WHERE ended_at IS NULL ORDER BY region`); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(activeIDs) != fmt.Sprint([]string{second.ID, europe.ID}) {
		t.Fatalf("active windows changed after failure: %v", activeIDs)
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM posting_windows`); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("failed window persisted: count=%d", count)
	}
}
