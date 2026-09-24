package isureal

import (
	"context"
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

func TestNotificationsIntegration(t *testing.T) {
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
	userID, otherID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'alice', 'hash', 'Alice', 'asia'), ($2, 'bob', 'hash', 'Bob', 'asia')`, userID, otherID)
	session, err := CreateLoginSession(db, userID)
	if err != nil {
		t.Fatal(err)
	}
	windowID, endedID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ($1, 'asia', '2026-09-23', clock_timestamp(), clock_timestamp(), NULL), ($2, 'asia', '2026-09-22', clock_timestamp(), clock_timestamp(), clock_timestamp())`, windowID, endedID)
	exec(`INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ($1, $2, $3, clock_timestamp()-interval '1 minute')`, uuid.NewString(), userID, windowID)
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.GET("/api/notifications", getNotificationsHandler(h), SessionMiddleware(h, true))
	server := httptest.NewServer(e)
	defer server.Close()
	connect := func() (*http.Response, context.CancelFunc) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/notifications", nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+session.Token)
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
	resp, cancel := connect()
	defer cancel()
	defer resp.Body.Close()
	exec(`INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ($1, $2, $3, clock_timestamp()), ($4, $5, $6, clock_timestamp())`, uuid.NewString(), userID, endedID, uuid.NewString(), otherID, windowID)
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := io.ReadAll(resp.Body); done <- result{data, err} }()
	select {
	case r := <-done:
		t.Fatalf("delivered an old, ended or other user's notification: %s %v", r.data, r.err)
	case <-time.After(700 * time.Millisecond):
	}
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id=$1`, windowID)
	newWindowID, notificationID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp())`, newWindowID)
	exec(`INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ($1, $2, $3, clock_timestamp())`, notificationID, userID, newWindowID)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	var notification getNotificationsResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(r.data), "data: "))), &notification); err != nil {
		t.Fatal(err)
	}
	if notification.ID != notificationID || notification.PostingWindowID != newWindowID {
		t.Fatalf("unexpected notification: %+v", notification)
	}
	resp2, cancel2 := connect()
	defer resp2.Body.Close()
	defer cancel2()
	go func() { data, err := io.ReadAll(resp2.Body); done <- result{data, err} }()
	select {
	case r := <-done:
		t.Fatalf("redelivered notification: %s %v", r.data, r.err)
	case <-time.After(700 * time.Millisecond):
	}
	cancel2()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	resp3, cancel3 := connect()
	defer resp3.Body.Close()
	defer cancel3()
	exec(`UPDATE users SET region='europe' WHERE id=$1`, userID)
	data, err := io.ReadAll(resp3.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("notification delivered after region change: %s", data)
	}

}
