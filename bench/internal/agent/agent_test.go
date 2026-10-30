package agent_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestRegisterValidatesResponse は、登録の 201 の応答のユーザー名が要求と違うときや、ログインの
// 応答が別人か空のトークンのとき、Register が ResponseError を返すことを確かめる。
func TestRegisterValidatesResponse(t *testing.T) {
	for _, tc := range []struct {
		name, registered, loginID, token string
		ok                               bool
	}{
		{"valid", "requested-user", "u1", "token", true},
		{"wrong username", "wrong-user", "u1", "token", false},
		{"empty username", "", "u1", "token", false},
		{"login as another user", "requested-user", "u2", "token", false},
		{"empty token", "requested-user", "u1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/auth/register":
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]any{"secretKey": "secret", "user": map[string]string{"id": "u1", "username": tc.registered}})
				case "/api/auth/login":
					logins.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"token": tc.token, "user": map[string]string{"id": tc.loginID, "username": "requested-user"}})
				default:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":"not_found"}`)
				}
			}))
			defer server.Close()
			a := agent.New(client.New(server.URL), "asia", 0)
			err := a.Register(t.Context(), "requested-user", "Requested User")
			if tc.ok {
				if err != nil || a.ID != "u1" || a.Username != "requested-user" || a.Token != "token" {
					t.Fatalf("agent=%+v err=%v", a, err)
				}
				return
			}
			var invalid *agent.ResponseError
			if !errors.As(err, &invalid) {
				t.Fatalf("err=%v (%T), want ResponseError", err, err)
			}
			if tc.registered != "requested-user" && logins.Load() != 0 {
				t.Fatal("logged in after an invalid registration")
			}
		})
	}
}
