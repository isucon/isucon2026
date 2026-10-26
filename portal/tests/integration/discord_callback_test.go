package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestDiscordCallbackCompatibility(t *testing.T) {
	for _, configuredPath := range []string{"", "/auth/discord/callback", "/auth/discord/complete/"} {
		t.Run(configuredPath, func(t *testing.T) {
			path := configuredPath
			if path == "" {
				path = "/auth/discord/callback"
			}
			t.Setenv("ISUXPORTAL_DISCORD_CALLBACK_PATH", configuredPath)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			p := testkit.Start(t)
			p.Cfg.TestMode = false
			p.Cfg.Discord = cfg.Discord
			exchanges := 0
			oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					exchanges++
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if got := r.Form.Get("redirect_uri"); got != p.Server.URL+path {
						t.Errorf("token redirect_uri = %q, want %q", got, p.Server.URL+path)
					}
					fmt.Fprint(w, `{"access_token":"test-token"}`)
				case "/users/@me":
					fmt.Fprint(w, `{"id":"5001","username":"alice","discriminator":"0"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer oauth.Close()
			p.Cfg.Discord.AuthorizeURL = oauth.URL + "/authorize"
			p.Cfg.Discord.TokenURL = oauth.URL + "/token"
			p.Cfg.Discord.OAuthProvider.APIBaseURL = oauth.URL
			client := registrationClient(t, p, &session.Session{})
			res := registrationRequest(t, p, client, "GET", "/auth/discord", nil, false, 302)
			authorize, err := url.Parse(res.Header.Get("Location"))
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got := authorize.Query().Get("redirect_uri"); got != p.Server.URL+path {
				t.Errorf("authorize redirect_uri = %q, want %q", got, p.Server.URL+path)
			}
			state := authorize.Query().Get("state")
			if state == "" {
				t.Fatal("missing OAuth state")
			}
			// Keep accepting the callback originally issued even if the preferred route changes.
			if path == "/auth/discord/callback" {
				p.Cfg.Discord.CallbackPath = "/auth/discord/complete/"
			} else {
				p.Cfg.Discord.CallbackPath = "/auth/discord/callback"
			}
			callback := path + "?" + url.Values{"state": {state}, "code": {"test-code"}}.Encode()
			res = registrationRequest(t, p, client, "GET", callback, nil, false, 302)
			res.Body.Close()
			if res.Header.Get("Location") != "/registration" || exchanges != 1 {
				t.Fatalf("callback location=%q, exchanges=%d", res.Header.Get("Location"), exchanges)
			}
			// A consumed state must not allow a second token exchange on either route.
			for _, rejected := range []string{callback, path + "?state=wrong&code=test-code"} {
				res = registrationRequest(t, p, client, "GET", rejected, nil, false, 302)
				res.Body.Close()
				if res.Header.Get("Location") != "/auth/failure" || exchanges != 1 {
					t.Fatalf("invalid state location=%q, exchanges=%d", res.Header.Get("Location"), exchanges)
				}
			}
		})
	}
}
