package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/session"
)

func TestAdminOnlyOperationalEndpoints(t *testing.T) {
	h := &Handler{cfg: &config.Config{AdminOnly: true}}
	next := h.adminOnlySwitch(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/site/sha", http.StatusNoContent},
		{"/admin/long_running_check", http.StatusNoContent},
		{"/", http.StatusFound},
		{"/admin/teams", http.StatusFound},
		{"/site/sha/private", http.StatusFound},
		{"/admin/long_running_check/private", http.StatusFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r = r.WithContext(context.WithValue(r.Context(), reqInfoKey, &reqInfo{sess: &session.Session{}}))
			w := httptest.NewRecorder()
			next.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
