package scenario

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/dataset"
)

// TestHistoryLoginValidation は、思い出を見返すユーザーのログインが 200 で別人の ID や空の
// トークンを返したとき、その応答を検証の失敗として打ち切りのエラー率に数えることを確かめる。
// 401 は正しさの違反、500 は Observer が 1 回だけ数える。
func TestHistoryLoginValidation(t *testing.T) {
	cfg := dataset.Small()
	cfg.Users, cfg.Days = 20, 1
	world := dataset.Generate(cfg)
	for _, u := range world.Users {
		u.Activity = 0
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, err := zw.Create("posts.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("[]"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "wrong-id", "empty-token", "401", "500"} {
		t.Run(mode, func(t *testing.T) {
			r, err := NewRunner(Config{Users: 1, Seed: 4967, Dataset: world, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			du := r.initial.HistoryUsers("asia")[0]
			var reads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case req.URL.Path == "/api/auth/login":
					switch mode {
					case "401":
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprint(w, `{"error":"invalid_credentials"}`)
						return
					case "500":
						w.WriteHeader(http.StatusInternalServerError)
						fmt.Fprint(w, `{"error":"internal_server_error"}`)
						return
					}
					id, token := du.ID, "valid-token"
					if mode == "wrong-id" {
						id = "another-user"
					}
					if mode == "empty-token" {
						token = ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{"id": id, "username": du.Username}, "token": token})
				case req.URL.Path == "/api/me/export":
					reads.Add(1)
					_, _ = w.Write(archive.Bytes())
				case strings.HasPrefix(req.URL.Path, "/api/memories"):
					reads.Add(1)
					fmt.Fprint(w, `{"items":[],"hasMore":false}`)
				case req.URL.Path == "/api/auth/logout":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected path %s", req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			rec := metrics.New(time.Now(), time.Minute)
			r.recorder.Store(rec)
			api := client.New(server.URL).WithCountry("JP")
			for range 20 {
				a := &agent.Agent{API: api.WithObserver(&accounting{r: r, slo: true}), ID: du.ID, Username: du.Username, SecretKey: du.SecretKey, Region: "asia"}
				r.revisit(t.Context(), &user{Agent: a, returning: true}, api.WithObserver(nil))
			}
			stats := rec.Total(0)
			switch mode {
			case "normal":
				if reads.Load() == 0 || stats.Failures != 0 || r.board.ErrorCount() != 0 || r.board.CriticalCount() != 0 {
					t.Fatalf("reads=%d stats=%+v errors=%v critical=%v", reads.Load(), stats, r.board.Errors(), r.board.Critical())
				}
			case "401":
				if r.board.CriticalCount() == 0 {
					t.Fatal("401 is not a violation")
				}
			default:
				if stats.Ops != 20 || stats.Failures != 20 || r.board.ErrorCount() != 20 || r.board.CriticalCount() != 0 {
					t.Fatalf("stats=%+v errors=%d critical=%d, want 20 failures of 20 logins", stats, r.board.ErrorCount(), r.board.CriticalCount())
				}
			}
		})
	}
}

// TestNewcomerRegistrationValidation は、走行中に来たユーザーの登録が 201 で ID かシークレット
// キーのない応答を返したとき、検証の失敗として数えることを確かめる。500 は Observer が 1 回
// だけ数える。
func TestNewcomerRegistrationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"empty", http.StatusCreated, `{}`},
		{"500", http.StatusInternalServerError, `{"error":"internal_server_error"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
			r.books.Store(newBook(r.tt, time.Now()))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.Path != "/api/auth/register" {
					t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			r.api = client.New(server.URL)
			rec := metrics.New(time.Now(), time.Minute)
			r.recorder.Store(rec)
			r.mu.Lock()
			r.arriving["asia"] += 20
			r.mu.Unlock()
			for range 20 {
				r.runNewcomer(t.Context(), "asia")
			}
			stats := rec.Total(0)
			if stats.Ops != 20 || stats.Failures != 20 || r.board.ErrorCount() != 20 {
				t.Fatalf("stats=%+v errors=%d, want 20 failures of 20 registrations", stats, r.board.ErrorCount())
			}
			if s := r.books.Load().stats("asia", 1); s.failures != 20 {
				t.Fatalf("growth failures=%d, want 20", s.failures)
			}
		})
	}
}

// observeAll は u の操作を打ち切りの判定と成長の判定の両方に数える計測を始め、記録先を返す。
func observeAll(r *Runner, u *user, base string) *metrics.Recorder {
	rec := metrics.New(time.Now(), time.Minute)
	r.recorder.Store(rec)
	r.books.Store(newBook(r.tt, time.Now()))
	u.API = client.New(base).WithCountry(u.Country()).WithObserver(&accounting{r: r, slo: true, growth: true})
	return rec
}

// routeFailures は rec のルート route の操作数と失敗数（全リージョンの合計）。
func routeFailures(rec *metrics.Recorder, route string) (ops, failures int64) {
	for _, rs := range rec.Routes() {
		if rs.Route == route {
			ops += rs.Ops
			failures += rs.Failures
		}
	}
	return ops, failures
}

// TestMalformedJSONCountsAsFailure は、200 で JSON として読めない応答を、打ち切りのエラー率、
// ルートごとの集計、成長の判定のそれぞれに 1 件の失敗として数えることを確かめる（二重に
// 数えない）。
func TestMalformedJSONCountsAsFailure(t *testing.T) {
	r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":`)
	}))
	defer server.Close()
	u := r.usersSnapshot()[0]
	rec := observeAll(r, u, server.URL)
	for range 20 {
		r.readFeed(t.Context(), u, "friends", nil, 1)
	}
	stats := rec.Total(0)
	if stats.Ops != 20 || stats.Failures != 20 {
		t.Fatalf("stats=%+v, want 20 failures of 20 operations", stats)
	}
	if ops, failures := routeFailures(rec, "GET /api/feeds/friends"); ops != 20 || failures != 20 {
		t.Fatalf("route ops=%d failures=%d", ops, failures)
	}
	if rec.Violation(metrics.DefaultSLO, 1) == "" {
		t.Fatal("malformed responses did not violate the SLO")
	}
	if s := r.books.Load().stats(u.Region, 1); s.ops != 20 || s.failures != 20 {
		t.Fatalf("growth ops=%d failures=%d", s.ops, s.failures)
	}
	if got := r.board.ErrorCount(); got != 20 {
		t.Fatalf("errors=%d, want 20", got)
	}
}

// TestDeletedSessionCheckCountsOnce は、退会したユーザーのセッションの確認が 401 以外で
// 終わったとき、その 1 操作を失敗として 1 回だけ数えることを確かめる。
func TestDeletedSessionCheckCountsOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		failures int64
	}{
		{"401", http.StatusUnauthorized, `{"error":"unauthorized"}`, 0},
		{"200", http.StatusOK, `{"id":"x"}`, 1},
		{"malformed 200", http.StatusOK, `{"id":`, 1},
		{"500", http.StatusInternalServerError, `{"error":"internal"}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/me/account-deletion" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			u := r.usersSnapshot()[0]
			rec := observeAll(r, u, server.URL)
			r.deleteAccount(t.Context(), u)
			if stats := rec.Total(0); stats.Ops != 2 || stats.Failures != tc.failures {
				t.Fatalf("stats=%+v, want %d failures", stats, tc.failures)
			}
			if s := r.books.Load().stats(u.Region, 1); s.failures != tc.failures {
				t.Fatalf("growth failures=%d, want %d", s.failures, tc.failures)
			}
		})
	}
}
