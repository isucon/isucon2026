package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	api := client.NewWithHost(server.URL, "app.127.0.0.1.nip.io")
	api.SetCountry("DE")
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
