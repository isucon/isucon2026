package scenario

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/score"
)

func TestInitializeSurvey(t *testing.T) {
	for _, tc := range []struct{ name, body, agent, harness string }{
		{"manual", `{"language":"go","agent":"human","harness":"hand"}`, "human", "hand"},
		{"custom", `{"language":"go","agent":"custom-model","harness":"custom-tool"}`, "custom-model", "custom-tool"},
		{"legacy", `{"language":"go"}`, "", ""},
		{"empty", `{"language":"go","agent":"","harness":""}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.Path != "/api/initialize" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var progress Result
			r := &Runner{cfg: Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnProgress: func(result Result) { progress = result }}, admin: client.New(server.URL), dataset: tinyDataset, board: score.NewBoard(score.DefaultWeights())}
			if reason := r.initialize(t.Context()); reason != "" {
				t.Fatal(reason)
			}
			r.progress()
			data, err := json.Marshal(progress)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			if body["language"] != "go" || body["agent"] != tc.agent || body["harness"] != tc.harness {
				t.Fatalf("survey was not preserved: %s", data)
			}
		})
	}
}

// A failure after initialization must still retain the survey in the final result.
func TestFinalResultRetainsInitializeSurvey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/initialize" {
			io.WriteString(w, `{"language":"go","agent":"custom-model","harness":"custom-tool"}`)
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	r, err := NewRunner(Config{Target: server.URL, Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	result := r.Run(t.Context())
	if result.Pass || result.Score != 0 || result.Agent != "custom-model" || result.Harness != "custom-tool" {
		t.Fatalf("unexpected final result: %+v", result)
	}
}
