package isureal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// The notification stream is served from the hot state: a window opened
// through the admin API reaches a stream connected before it, an older window
// is not delivered to a later stream, and a region change ends the stream.
func TestNotificationsIntegration(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	userID, otherID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'alice', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Alice', 'asia'), ($2, 'bob', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Bob', 'europe')`, userID, otherID)
	exec(`INSERT INTO administrators (id, username, password_hash) VALUES ('admin', 'admin', $1)`, mustHashPassword(t, "password1234"))
	session, err := CreateLoginSession(db, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := hot.load(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hot = newHotState() })
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db, Config: &Config{Region: "asia"}}
	e.GET("/api/notifications", getNotificationsHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.POST("/api/admin/posting-window", openPostingWindowHandler(h), AdminSessionMiddleware(h, true))
	e.POST("/api/admin/auth/login", adminLoginHandler(h))
	e.PATCH("/api/me", updateMeHandler(h), BatchSessionMiddleware(h, true))
	// Direct SQL edits below bypass the hot state; serve from the database.
	t.Setenv("ISUREAL_HOT_STATE", "off")
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAdmin(t, server, "admin", "password1234")
	openWindow := func(region, date string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"region": region, "localDate": date})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/admin/posting-window", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var window openPostingWindowResponse
		if err := json.NewDecoder(resp.Body).Decode(&window); err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatal("open window", resp.Status, err)
		}
		return window.ID
	}
	connect := func() (*http.Response, context.CancelFunc) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/notifications", nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+session.Token)
		req.Header.Set("X-Country-Code", "JP")
		resp, err := server.Client().Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
			resp.Body.Close()
			cancel()
			t.Fatal("unexpected response", resp.Status)
		}
		return resp, cancel
	}
	type result struct {
		data []byte
		err  error
	}
	read := func(resp *http.Response) chan result {
		done := make(chan result, 1)
		go func() { data, err := io.ReadAll(resp.Body); done <- result{data, err} }()
		return done
	}
	// A window of another region, opened before the stream, is not delivered.
	openWindow("europe", "2026-09-23")
	resp, cancel := connect()
	defer cancel()
	defer resp.Body.Close()
	done := read(resp)
	select {
	case r := <-done:
		t.Fatalf("delivered another region's notification: %s %v", r.data, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	windowID := openWindow("asia", "2026-09-23")
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	var notification getNotificationsResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(r.data), "data: "))), &notification); err != nil {
		t.Fatal(err)
	}
	if notification.ID != notificationID(windowID, notification.CreatedAt, userID) || notification.PostingWindowID != windowID || notification.Region != "asia" {
		t.Fatalf("unexpected notification: %+v", notification)
	}
	// The rows are written in the background with the same IDs.
	notificationWrites.drain()
	var rows int
	if err := db.Get(&rows, `SELECT count(*) FROM notifications WHERE posting_window_id = $1 AND id = $2 AND user_id = $3`, windowID, notification.ID, userID); err != nil || rows != 1 {
		t.Fatalf("notification rows: %d %v", rows, err)
	}
	// A stream connected after the opening does not receive it again.
	resp2, cancel2 := connect()
	defer resp2.Body.Close()
	defer cancel2()
	done2 := read(resp2)
	select {
	case r := <-done2:
		t.Fatalf("redelivered notification: %s %v", r.data, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel2()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	// A region change ends the stream without a notification.
	resp3, cancel3 := connect()
	defer resp3.Body.Close()
	defer cancel3()
	done3 := read(resp3)
	body, _ := json.Marshal(map[string]any{"region": "europe"})
	req, _ := http.NewRequest(http.MethodPatch, server.URL+"/api/me", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Content-Type", "application/json")
	update, err := server.Client().Do(req)
	if err != nil || update.StatusCode != http.StatusOK {
		t.Fatal("profile update", err, update.Status)
	}
	update.Body.Close()
	select {
	case r := <-done3:
		if r.err != nil || len(r.data) != 0 {
			t.Fatalf("notification delivered after region change: %s %v", r.data, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after region change")
	}
}

func mustHashPassword(t *testing.T, password string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}

func loginAdmin(t *testing.T, server *httptest.Server, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := server.Client().Post(server.URL+"/api/admin/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var response adminLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("admin login", resp.Status, err)
	}
	return response.Token
}
