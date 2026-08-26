package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDFormat(t *testing.T) {
	seen := make(map[string]bool, 100)
	for range 100 {
		id := client.NewUUID()
		if !uuidPattern.MatchString(id) {
			t.Fatalf("NewUUID() = %q, not a v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("NewUUID() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestLoginSendsContractHeaders(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "session-token",
			"user":  map[string]any{"id": "u1", "username": "alice"},
		})
	}))
	defer server.Close()

	api := client.New(server.URL)
	user, err := api.Login(context.Background(), "alice", "password")
	if err != nil {
		t.Fatal(err)
	}
	if user.Token != "session-token" {
		t.Fatalf("token = %q, want %q", user.Token, "session-token")
	}
	if got := captured.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := captured.Get("Idempotency-Key"); !uuidPattern.MatchString(got) {
		t.Fatalf("Idempotency-Key = %q, not a UUID", got)
	}
	if got := captured.Get("Cookie"); got != "" {
		t.Fatalf("Cookie header must be absent, got %q", got)
	}
	if got := captured.Get("Origin"); got != "" {
		t.Fatalf("Origin header must be absent, got %q", got)
	}
}

func TestHostOverride(t *testing.T) {
	var capturedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "session-token",
			"user":  map[string]any{"id": "u1", "username": "alice"},
		})
	}))
	defer server.Close()

	api := client.NewWithHost(server.URL, "app.team1-local.127.0.0.1.nip.io")
	if _, err := api.Login(context.Background(), "alice", "password"); err != nil {
		t.Fatal(err)
	}
	if capturedHost != "app.team1-local.127.0.0.1.nip.io" {
		t.Fatalf("Host = %q, want Vagrant application hostname", capturedHost)
	}
}

func TestUnexpectedStatusBecomesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"https://isureal.invalid/problems/username_taken","title":"Conflict","status":409,"code":"username_taken","correlationId":"x"}`))
	}))
	defer server.Close()

	api := client.New(server.URL)
	_, err := api.Register(context.Background(), "alice", "password", "Alice", "asia")
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "username_taken" {
		t.Fatalf("APIError = %+v, want status 409 code username_taken", apiErr)
	}
	if apiErr.Method != http.MethodPost || apiErr.Path != "/api/v1/auth/register" {
		t.Fatalf("APIError location = %s %s", apiErr.Method, apiErr.Path)
	}
}

func TestCreatePostBuildsMultipartWithLocationFile(t *testing.T) {
	var locationFilename, imageContentType, visibility string
	var locationBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if files := r.MultipartForm.File["location"]; len(files) == 1 {
			locationFilename = files[0].Filename
			file, _ := files[0].Open()
			locationBody = make([]byte, files[0].Size)
			_, _ = file.Read(locationBody)
			_ = file.Close()
		}
		if files := r.MultipartForm.File["image"]; len(files) == 1 {
			imageContentType = files[0].Header.Get("Content-Type")
		}
		visibility = r.FormValue("visibility")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"p1","imageUrl":"/api/v1/images/i1"}`))
	}))
	defer server.Close()

	api := client.New(server.URL)
	post, err := api.CreatePost(context.Background(), "token", []byte{0xFF, 0xD8, 0xFF}, "caption", "public")
	if err != nil {
		t.Fatal(err)
	}
	if post.ID != "p1" {
		t.Fatalf("post id = %q, want p1", post.ID)
	}
	if locationFilename == "" {
		t.Fatal("location part has no filename; app reads it via FormFile")
	}
	if string(locationBody) != `{"kind":"none"}` {
		t.Fatalf("location body = %q", locationBody)
	}
	if imageContentType != "image/jpeg" {
		t.Fatalf("image part Content-Type = %q, want image/jpeg", imageContentType)
	}
	if visibility != "public" {
		t.Fatalf("visibility = %q, want public", visibility)
	}
}
