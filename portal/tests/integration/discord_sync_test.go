package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// Synchronous jobs deliberately exercise the fastest possible worker: it must
// observe the new identity and must never revoke roles before the DB update.
func TestDiscordCallbackSynchronizesMembershipAndRoles(t *testing.T) {
	for _, scenario := range []string{"link", "switch", "login", "not-in-guild"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.Start(t)
			_, id := p.CreateTeam(t, "discord")
			oldID := "old"
			if scenario == "link" {
				oldID = ""
			}
			if scenario == "login" {
				oldID = "new"
			}
			if _, err := p.DB.Exec(`UPDATE contestants SET discord_id=$1, is_discord_guild_member=true WHERE id=$2`, oldID, id); err != nil {
				t.Fatal(err)
			}
			var calls []string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					fmt.Fprint(w, `{"access_token":"test-token"}`)
				case "/users/@me":
					fmt.Fprint(w, `{"id":"new","username":"new","discriminator":"0"}`)
				default:
					calls = append(calls, r.Method+" "+r.URL.Path)
					var storedID string
					if err := p.DB.QueryRow(`SELECT discord_id FROM contestants WHERE id=$1`, id).Scan(&storedID); err != nil {
						t.Error(err)
					}
					if storedID != "new" {
						t.Errorf("job observed stale identity %q", storedID)
					}
					if r.Method == "GET" {
						if strings.HasSuffix(r.URL.Path, "/old") {
							fmt.Fprint(w, `{"roles":["contestant"]}`)
						} else if scenario == "not-in-guild" {
							http.NotFound(w, r)
						} else {
							fmt.Fprint(w, `{"roles":[]}`)
						}
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				}
			}))
			defer api.Close()
			p.Cfg.Discord.AuthorizeURL = api.URL + "/authorize"
			p.Cfg.Discord.TokenURL = api.URL + "/token"
			p.Cfg.Discord.OAuthProvider.APIBaseURL = api.URL
			p.Cfg.Discord.ContestantRoleID = "contestant"
			p.Jobs.Discord.BotToken = "test"
			p.Jobs.Discord.APIBaseURL = api.URL
			p.Jobs.Discord.ServerID = "guild"
			client := registrationClient(t, p, &session.Session{ContestantID: id})
			res := registrationRequest(t, p, client, "GET", "/auth/discord", nil, false, 302)
			authorize, _ := url.Parse(res.Header.Get("Location"))
			res.Body.Close()
			res = registrationRequest(t, p, client, "GET", "/auth/discord/callback?"+url.Values{"state": {authorize.Query().Get("state")}, "code": {"test"}}.Encode(), nil, false, 302)
			res.Body.Close()
			var member bool
			if err := p.DB.QueryRow(`SELECT is_discord_guild_member FROM contestants WHERE id=$1`, id).Scan(&member); err != nil {
				t.Fatal(err)
			}
			if member != (scenario != "not-in-guild") {
				t.Errorf("membership = %v", member)
			}
			joined := strings.Join(calls, "\n")
			if !strings.Contains(joined, "GET /guilds/guild/members/new") {
				t.Errorf("new identity not synchronized: %s", joined)
			}
			if scenario != "not-in-guild" && !strings.Contains(joined, "PUT /guilds/guild/members/new/roles/contestant") {
				t.Errorf("new role not granted: %s", joined)
			}
			if oldID == "old" && !strings.Contains(joined, "DELETE /guilds/guild/members/old/roles/contestant") {
				t.Errorf("old role not revoked: %s", joined)
			}
			if (scenario == "login" || scenario == "link") && strings.Contains(joined, "DELETE ") {
				t.Errorf("unexpected revocation: %s", joined)
			}
		})
	}
}
