package scenario

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// 表示名と自己紹介の変更は、走行後の整合性チェックでプロフィールを読み直して確かめる。
// 応答だけ変えて保存しないアプリは失敗にし、結果のわからない変更はどちらの値でもよい。
func TestProfileUpdatesAreCheckedAfterRun(t *testing.T) {
	for _, mode := range []string{"normal", "not-saved", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			r, app := newTestRunner(t, 1, metrics.DefaultSLO)
			u := r.users["asia"][0]
			// 偽のアプリのユーザーは表示名と自己紹介が空で始まる。
			u.displayNames, u.bios = []string{""}, []string{""}
			routeThrough(t, r, u, app, func(w http.ResponseWriter, req *http.Request) bool {
				if mode == "normal" || req.Method != http.MethodPatch || req.URL.Path != "/api/me" {
					return false
				}
				var input map[string]string
				_ = json.NewDecoder(req.Body).Decode(&input)
				if mode == "unknown" {
					// 保存したうえで 500 を返す。
					app.mu.Lock()
					if name, ok := input["displayName"]; ok {
						app.users[u.ID].displayName = name
					}
					if bio, ok := input["bio"]; ok {
						app.users[u.ID].bio = bio
					}
					app.mu.Unlock()
					w.WriteHeader(http.StatusInternalServerError)
					return true
				}
				// 保存せず、送った値を応答だけに入れる。
				recorder := httptest.NewRecorder()
				get := httptest.NewRequest(http.MethodGet, "/api/me", nil)
				get.Header = req.Header.Clone()
				app.ServeHTTP(recorder, get)
				var profile map[string]any
				_ = json.Unmarshal(recorder.Body.Bytes(), &profile)
				for k, v := range input {
					profile[k] = v
				}
				_ = json.NewEncoder(w).Encode(profile)
				return true
			})
			r.rename(t.Context(), u)
			r.updateBio(t.Context(), u)
			err := r.finalCheckUser(t.Context(), u.Agent, nil)
			if (err != nil) != (mode == "not-saved") {
				t.Fatalf("final check: %v", err)
			}
		})
	}
}
