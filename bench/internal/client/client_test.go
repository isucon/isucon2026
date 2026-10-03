package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

func TestRegisterThenLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected legacy headers")
		}
		if r.Header.Get("X-Country-Code") != "DE" {
			t.Error("country header missing")
		}
		if r.Host != "app.127.0.0.1.nip.io" {
			t.Errorf("host=%s", r.Host)
		}
		switch r.URL.Path {
		case "/api/auth/register":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"secretKey":"secret","user":{"id":"u1","username":"alice"}}`)
		case "/api/auth/login":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["secretKey"] != "secret" {
				t.Error("wrong secret")
			}
			fmt.Fprint(w, `{"token":"token","user":{"id":"u1","username":"alice"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api := client.NewWithHost(server.URL, "app.127.0.0.1.nip.io").WithCountry("DE")
	registered, err := api.Register(t.Context(), "alice", "Alice", "europe")
	if err != nil || registered.SecretKey != "secret" || registered.Token != "" {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	user, err := api.Login(t.Context(), registered.Username, registered.SecretKey)
	if err != nil || user.Token != "token" || user.ID != registered.ID {
		t.Fatalf("user=%+v err=%v", user, err)
	}
}

func TestErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"username_taken"}`)
	}))
	defer server.Close()
	_, err := client.New(server.URL).Register(t.Context(), "alice", "Alice", "asia")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "username_taken" || apiErr.Path != "/api/auth/register" || apiErr.Want != 201 || apiErr.Status != 409 {
		t.Fatalf("err=%v", err)
	}
}

func TestPostMultipart(t *testing.T) {
	image := []byte{0xff, 0xd8, 0xff, 0xd9}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("location_kind") != "none" || len(r.MultipartForm.File["location"]) != 0 {
			t.Error("location must be regular form fields")
		}
		file, header, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, image) || header.Header.Get("Content-Type") != "image/jpeg" {
			t.Error("image changed")
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"p1","imageUrl":"/api/images/i1"}`)
	}))
	defer server.Close()
	post, err := client.New(server.URL).CreatePost(t.Context(), "token", image, "caption", "public")
	if err != nil || post.ID != "p1" {
		t.Fatalf("post=%+v err=%v", post, err)
	}
}

func TestPaginationAndOpen(t *testing.T) {
	before := client.Post{ID: "p1", PostedAt: time.Date(2026, 9, 30, 1, 2, 3, 123, time.UTC)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/posting-window" {
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["region"] != "asia" || input["localDate"] != "2026-10-01" || r.Method != http.MethodPost {
				t.Errorf("open=%v", input)
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"w1","region":"asia","localDate":"2026-10-01"}`)
			return
		}
		if r.URL.Query().Get("beforeId") != before.ID || r.URL.Query().Get("beforePostedAt") != before.PostedAt.Format(time.RFC3339Nano) || r.URL.Query().Has("cursor") {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"items":[],"hasMore":true}`)
	}))
	defer server.Close()
	api := client.New(server.URL)
	feed, err := api.Feed(t.Context(), "token", "public", &before)
	if err != nil || !feed.HasMore {
		t.Fatalf("feed=%+v err=%v", feed, err)
	}
	memory, err := api.MemoriesMonth(t.Context(), "token", "2026-09", &before)
	if err != nil || !memory.HasMore {
		t.Fatalf("memory=%+v err=%v", memory, err)
	}
	window, err := api.OpenWindow(t.Context(), "admin", "asia", "2026-10-01")
	if err != nil || window.ID != "w1" {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestNotificationSubscriptionEstablishedBeforeEvent(t *testing.T) {
	send := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("stream headers")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-send:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"n1\",\"postingWindowId\":\"w1\",\"region\":\"asia\"}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := client.New(server.URL).OpenNotifications(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	close(send)
	notification, err := stream.Receive()
	if err != nil || notification.ID != "n1" || notification.PostingWindowID != "w1" {
		t.Fatalf("notification=%+v err=%v", notification, err)
	}
}

func TestNotificationCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.New(server.URL).OpenNotifications(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	cancel()
	if _, err := stream.Receive(); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

type recorder struct {
	mu      sync.Mutex
	records []client.Record
}

func (r *recorder) Observe(record client.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
}

func (r *recorder) all() []client.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]client.Record(nil), r.records...)
}

func fastRetry(observer client.Observer, base string) *client.Client {
	return client.NewClient(client.Config{
		Base:     base,
		Retry:    client.RetryPolicy{MaxRetries: 2, Base: time.Millisecond, MaxWait: 20 * time.Millisecond},
		Observer: observer,
	}).WithCountry("JP")
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var followed atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/me", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := client.New(server.URL).Me(t.Context(), "token")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err=%v", err)
	}
	if followed.Load() {
		t.Fatal("redirect target was requested")
	}
}

func TestRetry503ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"body":"hi"}` {
			t.Errorf("retried body=%q", body)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	observed := &recorder{}
	if err := fastRetry(observed, server.URL).CreateComment(t.Context(), "token", "p1", "hi"); err != nil {
		t.Fatal(err)
	}
	records := observed.all()
	if len(records) != 1 {
		t.Fatalf("records=%+v", records)
	}
	record := records[0]
	if record.Route != "POST /api/posts/:postId/comments" || record.Attempts != 3 || !record.Saw503 || record.Err != nil || record.Status != 201 || record.Country != "JP" {
		t.Fatalf("record=%+v", record)
	}
}

func TestRetry503Exhausted(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	observed := &recorder{}
	_, err := fastRetry(observed, server.URL).Me(t.Context(), "token")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 || apiErr.Attempts != 3 || calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if records := observed.all(); len(records) != 1 || records[0].Err == nil || !records[0].Saw503 {
		t.Fatalf("records=%+v", records)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"id":"u1"}`)
	}))
	defer server.Close()
	start := time.Now()
	if _, err := fastRetry(nil, server.URL).Me(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Retry-After was not capped: %v", elapsed)
	}
}

func TestOtherStatusesAreNotRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, err := fastRetry(nil, server.URL).Me(t.Context(), "token"); err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestCanceledOperationIsNotObserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	observed := &recorder{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := fastRetry(observed, server.URL).Me(ctx, "token"); err == nil {
		t.Fatal("expected error")
	}
	if records := observed.all(); len(records) != 0 {
		t.Fatalf("records=%+v", records)
	}
}

func TestImageUsesSignedURLOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("image request must not send Authorization")
		}
		if r.URL.Query().Get("sig") != "s" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		fmt.Fprint(w, "jpeg")
	}))
	defer server.Close()
	observed := &recorder{}
	data, err := fastRetry(observed, server.URL).Image(t.Context(), "/api/images/i1?exp=1&sig=s")
	if err != nil || string(data) != "jpeg" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if records := observed.all(); len(records) != 1 || records[0].Route != "GET /api/images/:imageId" {
		t.Fatalf("records=%+v", records)
	}
}

func TestOwnConnections(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"u1"}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	base := client.New(server.URL)
	alice := base.WithCountry("JP").WithOwnConnections()
	bob := base.WithCountry("DE").WithOwnConnections()
	for range 3 {
		for _, api := range []*client.Client{alice, bob} {
			if _, err := api.Me(t.Context(), "token"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 各ユーザーは自分の keep-alive 接続を使い回し、他人の接続は使わない。
	if got := connections.Load(); got != 2 {
		t.Fatalf("connections=%d, want 2", got)
	}
}

func TestMayFailWith(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	observed := &recorder{}
	api := fastRetry(observed, server.URL)
	// 許したステータスでも、呼び出し側にはエラーとして返る。
	if err := api.CreateComment(client.MayFailWith(t.Context(), http.StatusForbidden), "token", "p1", "hi"); err == nil {
		t.Fatal("expected error")
	}
	if err := api.CreateComment(t.Context(), "token", "p1", "hi"); err == nil {
		t.Fatal("expected error")
	}
	records := observed.all()
	if len(records) != 2 || !records[0].Allowed || records[1].Allowed {
		t.Fatalf("records=%+v", records)
	}
}
