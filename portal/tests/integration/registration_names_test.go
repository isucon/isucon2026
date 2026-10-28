package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	resources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	admin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	registration "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/tests/testkit"
	"google.golang.org/protobuf/proto"
)

func TestRegistrationNameLimits(t *testing.T) {
	for _, route := range []string{"create-team", "create-contestant", "join", "update-team", "update-contestant", "admin-team", "admin-contestant"} {
		for _, char := range []string{"a", "名", "😀"} {
			for _, length := range []int{255, 256, 300} {
				t.Run(fmt.Sprintf("%s/%s/%d", route, char, length), func(t *testing.T) {
					p := testkit.Start(t)
					p.Cfg.TestMode = false
					p.Jobs.SetQueue(discardJobs{})
					now := time.Now()
					p.Cfg.Timing.RegistrationOpen = pointer(now.Add(-time.Hour))
					p.Cfg.Timing.RegistrationClose = pointer(now.Add(time.Hour))
					p.Cfg.Timing.RegistrationInvitationClose = pointer(now.Add(time.Hour))
					teamID, memberID := p.CreateTeam(t, "original")
					name := strings.Repeat(char, length)
					s := &session.Session{ContestantID: memberID}
					method, path := "PUT", "/api/registration"
					var msg proto.Message
					switch route {
					case "create-team", "create-contestant", "join":
						s = &session.Session{GitHubLogin: &session.OAuthIdentity{ID: "new-gh", Login: "new"}, DiscordLogin: &session.OAuthIdentity{ID: "new-dc", Tag: "new"}}
						method = "POST"
						consent := &registration.RegistrationRecordInput{AgreeTerms: pointer(true), AgreePrivacy: pointer(true)}
						if route == "join" {
							var invite string
							if err := p.DB.QueryRow(`SELECT invite_token FROM teams WHERE id=$1`, teamID).Scan(&invite); err != nil {
								t.Fatal(err)
							}
							path = "/api/registration/contestant"
							msg = &registration.JoinTeamRequest{TeamId: teamID, InviteToken: invite, Name: name, RegistrationRecord: consent}
						} else {
							path = "/api/registration/team"
							req := &registration.CreateTeamRequest{TeamName: "new", Name: "new", EmailAddress: "new@example.invalid", RegistrationRecord: consent}
							if route == "create-team" {
								req.TeamName = name
							} else {
								req.Name = name
							}
							msg = req
						}
					case "update-team", "update-contestant":
						req := &registration.UpdateRegistrationRequest{TeamName: "original", Name: "original-leader", EmailAddress: "original@test.invalid"}
						if route == "update-team" {
							req.TeamName = name
						} else {
							req.Name = name
						}
						msg = req
					default:
						s = &session.Session{Staff: true}
						path = fmt.Sprintf("/api/admin/teams/%d", teamID)
						req := &admin.UpdateTeamRequest{Team: &resources.Team{Id: teamID, Name: "original", LeaderId: memberID, Detail: &resources.Team_TeamDetail{EmailAddress: "original@test.invalid"}}}
						if route == "admin-team" {
							req.Team.Name = name
						} else {
							req.Contestants = []*resources.Contestant{{Id: memberID, Name: name, Detail: &resources.Contestant_ContestantDetail{GithubId: "gh-original", GithubLogin: "original-gh", DiscordId: "dc-original", DiscordTag: "original#0001"}}}
						}
						msg = req
					}
					status := 200
					if length > 255 {
						status = 422
					}
					res := registrationRequest(t, p, registrationClient(t, p, s), method, path, msg, true, status)
					body := readAll(t, res)
					if length > 255 {
						if !strings.Contains(body, "255") {
							t.Fatalf("missing limit in error: %s", body)
						}
						var teamName, memberName string
						if err := p.DB.QueryRow(`SELECT t.name, c.name FROM teams t JOIN contestants c ON c.id=t.leader_id WHERE t.id=$1`, teamID).Scan(&teamName, &memberName); err != nil {
							t.Fatal(err)
						}
						if teamName != "original" || memberName != "original-leader" {
							t.Fatalf("invalid request changed names: %q %q", teamName, memberName)
						}
					}
				})
			}
		}
	}
}
