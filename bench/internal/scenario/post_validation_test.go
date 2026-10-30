package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

func TestPostValidationSurvivesNewSessionAndRejectsDataLoss(t *testing.T) {
	lost := false
	initialized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/initialize":
			initialized = true
			t.Error("post validation initialized application")
		case "/api/auth/login":
			w.Write([]byte(`{"user":{"id":"u1","username":"alice"},"token":"fresh"}`))
		case "/api/me":
			w.Write([]byte(`{"id":"u1","username":"alice","region":"asia","displayName":"Alice"}`))
		case "/api/memories":
			if lost {
				w.Write([]byte(`{"items":[],"hasMore":false}`))
			} else {
				w.Write([]byte(`{"items":[{"id":"p1","postingWindowId":"w1","caption":"saved"}],"hasMore":false}`))
			}
		case "/api/relationships/friends":
			w.Write([]byte(`{"items":[],"hasMore":false}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := &agent.Agent{ID: "u1", Username: "alice", SecretKey: "secret", Token: "old", Region: "asia", API: client.New(server.URL)}
	snapshot, err := captureValidationUser(context.Background(), a, []string{"2026-10"})
	if err != nil {
		t.Fatal(err)
	}
	saved := ValidationSnapshot{Version: 1, Users: []ValidationUser{snapshot}}
	if err := ValidateSnapshot(context.Background(), client.New(server.URL), saved); err != nil {
		t.Fatal(err)
	}
	lost = true
	if err := ValidateSnapshot(context.Background(), client.New(server.URL), saved); err == nil {
		t.Fatal("lost post passed")
	}
	if initialized {
		t.Fatal("initialized")
	}
	if err := ValidateSnapshot(context.Background(), client.New(server.URL), ValidationSnapshot{Version: 1}); err == nil {
		t.Fatal("empty evidence passed")
	}
}

func TestLoadEvidenceCanBeValidatedByNewClient(t *testing.T) {
	runner, _ := newTestRunner(t, 2, metrics.SLO{FailRate: 1, MinOps: 1})
	if reason := runner.load(t.Context()); reason != "" {
		t.Fatal(reason)
	}
	saved, err := runner.validationSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Users) < 3 {
		t.Fatal("missing regional evidence")
	}
	body, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	var restored ValidationSnapshot
	if err = json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if err = ValidateSnapshot(t.Context(), runner.api, restored); err != nil {
		t.Fatal(err)
	}
}
