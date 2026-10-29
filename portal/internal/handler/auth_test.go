package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/session"
)

func TestSafeBackTo(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		want bool
	}{
		{"root", "/", true},
		{"invitation", "/registration?invite_token=test-token", true},
		{"query and fragment", "/registration?next=%2Fteam#join", true},
		{"empty", "", false},
		{"relative", "registration", false},
		{"absolute", "https://example.com/audit", false},
		{"network path", "//example.com/audit", false},
		{"backslash authority", `/\127.0.0.1:8083/audit`, false},
		{"double backslash", `/\\127.0.0.1:8083/audit`, false},
		{"backslash in path", `/registration\invitation`, false},
		{"tab", "/\t/example.com", false},
		{"newline", "/\n/example.com", false},
		{"carriage return", "/\r/example.com", false},
		{"invalid escape", "/%zz", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Match the query decoding performed by sessionNew.
			query, err := url.ParseQuery(url.Values{"back_to": {tt.path}}.Encode())
			if err != nil {
				t.Fatal(err)
			}
			if got := safeBackTo(query.Get("back_to")); got != tt.want {
				t.Errorf("safeBackTo(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestRedirectAfterAuthValidatesStoredBackTo(t *testing.T) {
	for _, tt := range []struct {
		backTo string
		want   string
	}{
		{"", "/registration"},
		{`/\127.0.0.1:8083/audit`, "/registration"},
		{`/\\127.0.0.1:8083/audit`, "/registration"},
		{"//example.com/audit", "/registration"},
		{"https://example.com/audit", "/registration"},
		{"/registration?invite_token=test-token", "/registration?invite_token=test-token"},
	} {
		t.Run(tt.backTo, func(t *testing.T) {
			h := &Handler{store: &session.Store{}}
			r := httptest.NewRequest(http.MethodGet, "/auth/github/callback", nil)
			// Model a previously persisted session without requiring Redis.
			r = r.WithContext(context.WithValue(r.Context(), reqInfoKey, &reqInfo{
				sess: &session.Session{BackTo: tt.backTo},
			}))
			w := httptest.NewRecorder()
			h.redirectAfterAuth(w, r, "/registration")
			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
			}
			if got := w.Header().Get("Location"); got != tt.want {
				t.Errorf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}
