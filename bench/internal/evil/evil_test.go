package evil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/scenario"
	"github.com/isucon/isucon2026/dataset"
)

func TestPlans(t *testing.T) {
	env := scenario.EvilEnv{Start: time.Unix(42, 0), Length: time.Minute, Seed: 7}
	a, b := plans(env), plans(env)
	if !reflect.DeepEqual(a, b) || len(a) != 45 {
		t.Fatal("plans are not deterministic or arrivals are missing")
	}
	for i, p := range a {
		cycle, slot := i/4, i%4
		wantAt := time.Duration(cycle) * arrivalInterval
		wantKind := 1
		if slot == 1 || slot == 2 {
			wantAt += time.Duration(slot) * arrivalInterval / 4
			wantKind = 2
		} else if slot == 3 {
			wantAt += 3 * arrivalInterval / 4
			wantKind = 3
		}
		if i == len(a)-1 {
			cycle, slot = 11, 0
			wantAt, wantKind = time.Duration(cycle)*arrivalInterval, 1
		}
		if p.at != wantAt || p.kind != wantKind {
			t.Fatalf("plan %d: %+v", i, p)
		}
		if p.kind == 2 && p.uploadInterval != 500*time.Millisecond {
			t.Fatalf("invalid upload interval: %+v", p)
		}
		if p.kind == 3 && (p.rate != 50 || p.targetIndex != cycle) {
			t.Fatalf("invalid signature actor: %+v", p)
		}
		if p.kind == 1 && (!strings.HasPrefix(p.password, "password_dummy_") ||
			(i == len(a)-1 && p.rate != 300) ||
			(i != len(a)-1 && (p.rate < 40 || p.rate > 160))) {
			t.Fatalf("invalid login actor: %+v", p)
		}
	}
}

func TestSignatureActorUsesExistingImageAndStopsOnBan(t *testing.T) {
	world := &dataset.World{Users: []*dataset.User{
		{Status: "deleted", SecretKey: "deleted-secret", ProfileImage: &dataset.Image{ID: "deleted-image"}},
		{Status: "active", SecretKey: "owner-secret", ProfileImage: &dataset.Image{ID: "real-image"}},
	}}
	var count atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/images/real-image" || r.Header.Get("X-Client-IP") != "192.0.2.3" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("X-Client-IP"))
		}
		query := r.URL.Query()
		exp, err := strconv.ParseInt(query.Get("exp"), 10, 64)
		if err != nil || exp <= time.Now().Unix() || len(query.Get("sig")) != 43 || query.Get("sig") == imageurl.Sign("owner-secret", "real-image", exp) {
			t.Errorf("invalid forged signature: %v", query)
		}
		if count.Add(1) == 4 {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"ip_banned"}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"image_forbidden"}`)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runSignatureActor(ctx, scenario.EvilEnv{Start: time.Now(), Dataset: world}, api.WithClientIP("192.0.2.3"), actorPlan{kind: 3, rate: 50})
	if n := count.Load(); n < 4 || n > 5 {
		t.Fatalf("continued after ban: %d", n)
	}
}

func TestForgedImageURLRejectsMissingTarget(t *testing.T) {
	if _, ok := forgedImageURL(nil, 0); ok {
		t.Fatal("nil dataset has image URL")
	}
	if _, ok := forgedImageURL(&dataset.World{}, 0); ok {
		t.Fatal("empty dataset has image URL")
	}
	world := &dataset.World{Users: []*dataset.User{{Status: "active", SecretKey: "s", ProfileImage: &dataset.Image{ID: "id with space"}}}}
	path, ok := forgedImageURL(world, 0)
	parsed, err := url.Parse(path)
	if !ok || err != nil || parsed.Path != "/api/images/id with space" {
		t.Fatalf("invalid image URL: %q %v", path, err)
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
			if r.Header.Get("Authorization") != "Bearer own-token" || r.Header.Get("Content-Type") != "image/jpeg" || r.ContentLength != 31<<20 {
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
	}}, api, actorPlan{kind: 2, username: "evil_upload", uploadInterval: 10 * time.Millisecond})
	if registered.Load() != 1 || uploads.Load() != 2 {
		t.Fatal(registered.Load(), uploads.Load())
	}
}

func TestUploadActorStopsAfterFixedCount(t *testing.T) {
	var uploads atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/register":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"secretKey":"own-secret","user":{"id":"evil-id"}}`)
		case "/api/auth/login":
			fmt.Fprint(w, `{"token":"own-token"}`)
		case "/api/me/profile-image":
			uploads.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_profile_image"}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runUploadActor(ctx, scenario.EvilEnv{}, api, actorPlan{kind: 2, username: "evil_upload", uploadInterval: 10 * time.Millisecond})
	if got := uploads.Load(); got != type2UploadCount {
		t.Fatalf("uploaded %d times, want %d", got, type2UploadCount)
	}
}

func TestStreamCanBeRejectedFromHeaderBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 31<<20 {
			t.Errorf("content length = %d, want %d", r.ContentLength, 31<<20)
		}
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
