package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestUserReadIntegration(t *testing.T) {
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
	for _, id := range []string{"viewer", "target"} {
		exec(`INSERT INTO users (id,username,secret_key_hash,display_name,bio,region) VALUES ($1,$2,'hash',$3,'private','asia')`, id, id, "Café Straße")
	}
	session, err := CreateLoginSession(db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/users", searchUsersHandler(h), SessionMiddleware(h, true))
	e.GET("/api/users/:userId", getUserHandler(h), SessionMiddleware(h, true))
	request := func(path string, status int, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+session.Token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec
	}
	search := func(q string) searchUsersResponse {
		t.Helper()
		rec := request("/api/users?q="+url.QueryEscape(q), http.StatusOK, true)
		var out searchUsersResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	profile := func() getUserResponse {
		t.Helper()
		rec := request("/api/users/target", http.StatusOK, true)
		var out getUserResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	request("/api/users?q=x", http.StatusUnauthorized, false)
	request("/api/users/target", http.StatusUnauthorized, false)
	request("/api/users", http.StatusBadRequest, true)
	request("/api/users?q=x&beforeId=x", http.StatusBadRequest, true)
	request("/api/users/missing", http.StatusNotFound, true)
	for _, q := range []string{"CAFE\u0301", "STRASSE", "TARGET"} {
		out := search(q)
		if len(out.Items) != 1 || out.Items[0].ID != "target" || out.Items[0].Bio != "" {
			t.Fatalf("search %q: %+v", q, out)
		}
	}
	for _, q := range []string{"%", "_", "\\"} {
		out := search(q)
		if out.Items == nil || len(out.Items) != 0 {
			t.Fatalf("literal query %q: %+v", q, out)
		}
	}
	if profile().Bio != "" {
		t.Fatal("nonfriend bio exposed")
	}
	exec(`INSERT INTO friendships (id,user_low_id,user_high_id) VALUES ('00000000-0000-0000-0000-000000000001','target','viewer')`)
	if profile().Bio != "private" {
		t.Fatal("friend bio hidden")
	}
	if search("target").Items[0].Bio != "" {
		t.Fatal("search disclosed regular bio")
	}
	exec(`DELETE FROM friendships`)
	exec(`UPDATE users SET account_type='certified_public' WHERE id='target'`)
	if profile().Bio != "private" || search("target").Items[0].Bio != "private" {
		t.Fatal("certified bio hidden")
	}
	for _, pair := range [][2]string{{"viewer", "target"}, {"target", "viewer"}} {
		exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES ('00000000-0000-0000-0000-000000000002',$1,$2)`, pair[0], pair[1])
		if len(search("target").Items) != 0 {
			t.Fatal("blocked user searchable")
		}
		request("/api/users/target", http.StatusNotFound, true)
		exec(`DELETE FROM block_relations`)
	}
	exec(`UPDATE users SET status='deleted' WHERE id='target'`)
	request("/api/users/target", http.StatusNotFound, true)
	if len(search("target").Items) != 0 {
		t.Fatal("deleted user searchable")
	}
	for i := 0; i < 52; i++ {
		id := fmt.Sprintf("page-%02d", i)
		exec(`INSERT INTO users(id,username,secret_key_hash,display_name,region) VALUES ($1,'page','hash','Page','asia')`, id)
	}
	first := search("page")
	if len(first.Items) != 50 || !first.HasMore {
		t.Fatalf("page 1: %+v", first)
	}
	last := first.Items[49]
	rec := request("/api/users?q=page&beforeUsername="+last.Username+"&beforeId="+last.ID, http.StatusOK, true)
	var second searchUsersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.HasMore || second.Items[0].ID <= last.ID {
		t.Fatalf("page 2: %+v", second)
	}
}
