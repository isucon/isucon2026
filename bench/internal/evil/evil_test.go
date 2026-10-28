package evil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/scenario"
)

func TestPlans(t *testing.T) {
	env := scenario.EvilEnv{Start: time.Unix(42, 0), Length: time.Minute, Seed: 7}
	a, b := plans(env), plans(env)
	if !reflect.DeepEqual(a, b) || len(a) != 24 {
		t.Fatal("plans are not deterministic or arrivals are missing")
	}
	for i, p := range a {
		if p.at != time.Duration(i)*arrivalInterval/2 || p.kind != i%2+1 {
			t.Fatalf("plan %d: %+v", i, p)
		}
		if p.kind == 1 && (p.rate < 20 || p.rate > 100 || !strings.HasPrefix(p.password, "password_dummy_")) {
			t.Fatalf("invalid login actor: %+v", p)
		}
	}
}

func TestLoginActorNeverRegistersAndStopsOnBan(t *testing.T) {
	var count atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" || r.Header.Get("X-Client-IP") != "192.0.2.1" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(body["username"], "evil_7_") || body["secretKey"] != "password_dummy_42" {
			t.Error(body)
		}
		if count.Add(1) == 4 {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":"ip_banned"}`)
			return
		}
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid_credentials"}`)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runLoginActor(ctx, scenario.EvilEnv{Start: time.Now()}, api.WithClientIP("192.0.2.1"), actorPlan{kind: 1, username: "evil_7", password: "password_dummy_42", rate: 100})
	if n := count.Load(); n < 4 || n > 5 {
		t.Fatalf("continued after ban: %d", n)
	}
}

func TestUploadActorStreamsNonJPEGAndStopsOnBan(t *testing.T) {
	var uploads, registered atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/register":
			registered.Add(1)
			w.WriteHeader(201)
			fmt.Fprint(w, `{"secretKey":"own-secret","user":{"id":"evil-id"}}`)
		case "/api/auth/login":
			fmt.Fprint(w, `{"token":"own-token"}`)
		case "/api/me/profile-image":
			if r.Header.Get("Authorization") != "Bearer own-token" || r.Header.Get("Content-Type") != "image/jpeg" || r.ContentLength > 0 {
				t.Error("stream headers are wrong", r.Header, r.ContentLength)
			}
			magic := make([]byte, 12)
			if _, err := io.ReadFull(r.Body, magic); err != nil || string(magic[4:8]) != "ftyp" {
				t.Error("not an MP4 stream", magic, err)
			}
			if uploads.Add(1) == 2 {
				w.WriteHeader(403)
				fmt.Fprint(w, `{"error":"ip_banned"}`)
			} else {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_profile_image"}`)
			}
		default:
			t.Error("unexpected path", r.URL.Path)
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runUploadActor(ctx, scenario.EvilEnv{RegisterUser: func(id string) {
		if id != "evil-id" {
			t.Error(id)
		}
	}}, api, actorPlan{kind: 2, username: "evil_upload"})
	if registered.Load() != 1 || uploads.Load() != 2 {
		t.Fatal(registered.Load(), uploads.Load())
	}
}

func TestStreamCanBeRejectedFromHeaderBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		magic := make([]byte, 12)
		if _, err := io.ReadFull(r.Body, magic); err != nil || string(magic[4:8]) != "ftyp" {
			t.Error("bad streamed prefix", magic, err)
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_profile_image"}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err := client.New(server.URL).PutNonJPEGStream(ctx, "token")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("early rejection failed: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("client waited for the full stream")
	}
}

type handlerTransport struct{ handler http.Handler }

func (tr handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	tr.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func testAPI(handler http.Handler) *client.Client {
	return client.NewClient(client.Config{Base: "http://evil.test", Transport: handlerTransport{handler}})
}
