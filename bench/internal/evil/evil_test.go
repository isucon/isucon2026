package evil

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/scenario"
	"github.com/isucon/isucon2026/dataset"
)

func TestPlans(t *testing.T) {
	env := scenario.EvilEnv{Start: time.Unix(42, 123), Length: time.Minute, Dataset: &dataset.World{Users: []*dataset.User{{Username: "normal"}}}}
	a, b := plans(env), plans(env)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("non-deterministic plans")
	}
	for i, want := range []time.Duration{10 * time.Second, 25 * time.Second, 40 * time.Second} {
		if a[i].at != want {
			t.Fatalf("start %d: %v", i, a[i].at)
		}
	}
	env.Start = env.Start.Add(time.Second)
	if reflect.DeepEqual(a, plans(env)) {
		t.Fatal("seed ignored")
	}
}

func TestActorBehaviors(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	var registered atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Client-IP") != "192.0.2.17" || r.Header.Get("X-Country-Code") != "JP" {
			t.Error("actor identity changed")
		}
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/api/auth/register":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "evil_test" || body["region"] != "asia" {
				t.Error(body)
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"secretKey":"own-secret","user":{"id":"evil-id"}}`)
		case "/api/auth/login":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "evil_test" {
				t.Error("normal login")
			}
			if body["secretKey"] == "own-secret" {
				fmt.Fprint(w, `{"token":"own-token","user":{"id":"evil-id"}}`)
			} else {
				if body["secretKey"] != "invalid-evil-credential" {
					t.Error("unexpected credential")
				}
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"invalid_credentials"}`)
			}
		case "/api/users":
			if registered.Load() != 1 || r.Header.Get("Authorization") != "Bearer own-token" || r.URL.Query().Get("q") == "" {
				t.Error("invalid enumeration")
			}
			fmt.Fprint(w, `{"items":[],"hasMore":false}`)
		case "/api/me/profile-image":
			if r.Method != "PUT" || r.Header.Get("Authorization") != "Bearer own-token" || r.Header.Get("Content-Type") != "image/jpeg" {
				t.Error("invalid upload request")
			}
			img, err := jpeg.Decode(r.Body)
			if err != nil {
				t.Error(err)
			} else if img.Bounds().Dx() <= 512 {
				t.Error("valid upload")
			}
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_profile_image"}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
	}))

	env := scenario.EvilEnv{Start: time.Now(), RegisterUser: func(id string) {
		if id != "evil-id" {
			t.Error(id)
		}
		registered.Add(1)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	runActor(ctx, env, api.WithCountry("JP").WithClientIP("192.0.2.17"), actorPlan{username: "evil_test", queries: []string{"normal", "a"}}, 20*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if registered.Load() != 1 || counts["/api/auth/register"] != 1 || counts["/api/auth/login"] < 10 || counts["/api/users"] < 2 || counts["/api/me/profile-image"] < 2 {
		t.Fatal(counts, registered.Load())
	}
}

func TestBanAndOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		stop   bool
	}{
		{"banned", 403, `{"error":"ip_banned"}`, true},
		{"other-forbidden", 403, `{"error":"region_mismatch"}`, false},
		{"wrong-status", 401, `{"error":"ip_banned"}`, false},
		{"malformed", 403, `not json`, false},
		{"server-error", 500, `{"error":"internal"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int64
			api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			runActor(ctx, scenario.EvilEnv{Start: time.Now()}, api, actorPlan{username: "evil", queries: []string{"a"}}, 20*time.Millisecond)
			if tc.stop && count.Load() != 1 {
				t.Fatal("did not stop", count.Load())
			}
			if !tc.stop && count.Load() < 3 {
				t.Fatal("stopped on non-ban", count.Load())
			}
		})
	}
}

func TestOpenLoopAndCancellation(t *testing.T) {
	var active, peak atomic.Int64
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-r.Context().Done()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	runActor(ctx, scenario.EvilEnv{Start: start}, api, actorPlan{username: "evil", queries: []string{"a"}}, 10*time.Millisecond)
	if peak.Load() < 3 || peak.Load() > 8 {
		t.Fatal("not bounded open loop", peak.Load())
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation delayed")
	}
}

func TestRunRampAndIPs(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]time.Time{}
	api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("X-Client-IP")] = time.Now()
		mu.Unlock()
		if r.Header.Get("X-Country-Code") != "JP" {
			t.Error("country")
		}
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":"ip_banned"}`)
	}))

	var allocated int
	start := time.Now()
	env := scenario.EvilEnv{API: api, Start: start, Length: 360 * time.Millisecond, NextIP: func() string { allocated++; return fmt.Sprintf("192.0.2.%d", allocated) }}
	Run(context.Background(), env)
	mu.Lock()
	defer mu.Unlock()
	if allocated != 3 || len(seen) != 3 {
		t.Fatal(allocated, seen)
	}
	for i, p := range plans(env) {
		ip := fmt.Sprintf("192.0.2.%d", i+1)
		if seen[ip].Before(start.Add(p.at)) {
			t.Fatal("early actor", ip)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.NextIP = func() string { t.Error("allocated after cancellation"); return "" }
	Run(ctx, env)
}

// handlerTransport はソケットを使わずに HTTP の応答を作る。
type handlerTransport struct{ handler http.Handler }

func (tr handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	tr.handler.ServeHTTP(rec, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}

func testAPI(handler http.Handler) *client.Client {
	return client.NewClient(client.Config{Base: "http://evil.test", Transport: handlerTransport{handler}})
}

func TestBanDuringMisuse(t *testing.T) {
	for _, path := range []string{"/api/auth/login", "/api/users", "/api/me/profile-image"} {
		t.Run(path, func(t *testing.T) {
			var banned atomic.Bool
			var afterBan atomic.Int64
			api := testAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if banned.Load() {
					afterBan.Add(1)
				}
				if r.URL.Path == path {
					banned.Store(true)
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":"ip_banned"}`)
					return
				}
				switch r.URL.Path {
				case "/api/auth/register":
					w.WriteHeader(201)
					fmt.Fprint(w, `{"secretKey":"own","user":{"id":"evil"}}`)
				case "/api/auth/login":
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["secretKey"] == "own" {
						fmt.Fprint(w, `{"token":"own-token"}`)
					} else {
						w.WriteHeader(401)
					}
				case "/api/users":
					fmt.Fprint(w, `{"items":[]}`)
				default:
					w.WriteHeader(400)
				}
			}))
			// 誤用の段階に入って BAN されるまでの余裕。-race で並列に走らせても足りるように長めに取る。
			// BAN されればすぐに止まるので、通常は短く終わる。
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runActor(ctx, scenario.EvilEnv{Start: time.Now()}, api, actorPlan{username: "evil", queries: []string{"a"}}, 20*time.Millisecond)
			if !banned.Load() || ctx.Err() != nil {
				t.Fatal("actor did not stop on ban")
			}
			// 同じ枠で送ったログインは BAN の応答と行き違うことがある。
			if afterBan.Load() > 1 {
				t.Fatal("continued after ban", afterBan.Load())
			}
		})
	}
}
