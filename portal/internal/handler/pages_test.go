package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/session"
)

func TestBroadcastShellBundles(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{
		"application.js": "/packs/application-test.js",
		"vendor.js": "/packs/vendor-test.js",
		"broadcast_view.js": "/packs/broadcast_view-test.js",
		"broadcast_view.css": "/packs/broadcast_view-test.css",
		"audience.js": "/packs/audience-test.js"
	}`), 0600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		cfg:   &config.Config{TestMode: true, PacksManifest: manifest},
		store: &session.Store{},
	}
	for _, path := range []string{"/broadcast_view/leaderboard", "/broadcast_view/score_changes", "/broadcast_view/clock"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			// An unchanged session avoids requiring Redis for this HTML regression test.
			r = r.WithContext(context.WithValue(r.Context(), reqInfoKey, &reqInfo{
				sess: &session.Session{CSRFToken: "test-csrf"},
			}))
			w := httptest.NewRecorder()
			h.BroadcastShell(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
			}
			html := w.Body.String()
			for _, tag := range []string{
				`<script src="/packs/application-test.js"></script>`,
				`<script src="/packs/vendor-test.js"></script>`,
				`<script src="/packs/broadcast_view-test.js"></script>`,
				`<link rel="stylesheet" href="/packs/broadcast_view-test.css">`,
			} {
				if strings.Count(html, tag) != 1 {
					t.Errorf("expected exactly one %s", tag)
				}
			}
			if strings.Contains(html, "/packs/audience-test.js") {
				t.Error("broadcast shell loads audience app, which overwrites the broadcast app")
			}
		})
	}
}
