package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/tests/testkit"
	"google.golang.org/protobuf/proto"
)

type discardJobs struct{}

func (discardJobs) Enqueue(context.Context, string, any) error { return nil }

func registrationClient(t *testing.T, p *testkit.Portal, s *session.Session) *http.Client {
	t.Helper()
	store, err := session.NewStore(p.Cfg.RedisURL, p.Cfg.SessionSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Redis().Close()
	s.CSRFToken = "test-csrf"
	s.MarkDirty()
	w := httptest.NewRecorder()
	if err := store.Save(context.Background(), w, s); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(p.Server.URL)
	jar.SetCookies(u, w.Result().Cookies())
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func registrationRequest(t *testing.T, p *testkit.Portal, client *http.Client, method, path string, msg proto.Message, csrf bool, status int) *http.Response {
	t.Helper()
	var raw []byte
	var err error
	if msg != nil {
		raw, err = proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, p.Server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if csrf {
		req.Header.Set("X-Csrf-Token", "test-csrf")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Fatalf("%s %s = %d, expected %d: %s", method, path, res.StatusCode, status, readAll(t, res))
	}
	return res
}

func TestRegistrationRecordHTTP(t *testing.T) {
	p := testkit.Start(t)
	p.Cfg.TestMode = false
	p.Jobs.SetQueue(discardJobs{})
	now := time.Now()
	p.Cfg.Timing.RegistrationOpen = pointer(now.Add(-time.Hour))
	p.Cfg.Timing.RegistrationClose = pointer(now.Add(time.Hour))
	p.Cfg.Timing.RegistrationInvitationClose = pointer(now.Add(time.Hour))
	client := registrationClient(t, p, &session.Session{GitHubLogin: &session.OAuthIdentity{ID: "1001", Login: "alice"}, DiscordLogin: &session.OAuthIdentity{ID: "2001", Tag: "alice"}})
	input := &pb.RegistrationRecordInput{AgreeTerms: pointer(true), AgreePrivacy: pointer(true), AgreeSponsor: pointer(true), MinorParentalConsent: pointer(false), Email: pointer("alice@example.invalid"), TshirtSize: pointer("M")}
	created := &pb.CreateTeamResponse{}
	testkit.DecodePB(t, registrationRequest(t, p, client, "POST", "/api/registration/team", &pb.CreateTeamRequest{TeamName: "normal", Name: "Alice", EmailAddress: "team@example.invalid", RegistrationRecord: input}, true, 200), created)
	got := &pb.RegistrationRecord{}
	testkit.DecodePB(t, registrationRequest(t, p, client, "GET", "/api/registration/record", nil, false, 200), got)
	if got.TermsAgreedAt == nil || got.PrivacyAgreedAt == nil || got.SponsorProvideAgreedAt == nil || got.MinorParentalConsent == nil || *got.MinorParentalConsent || got.Email != "alice@example.invalid" {
		t.Fatalf("registration record=%+v", got)
	}
	original := got.GetTermsAgreedAt()
	registrationRequest(t, p, client, "PUT", "/api/registration", &pb.UpdateRegistrationRequest{TeamName: "normal", Name: "Alice", EmailAddress: "team@example.invalid", RegistrationRecord: &pb.RegistrationRecordInput{Email: pointer("updated@example.invalid"), TshirtSize: pointer("L")}}, true, 200).Body.Close()
	testkit.DecodePB(t, registrationRequest(t, p, client, "GET", "/api/registration/record", nil, false, 200), got)
	if got.Email != "updated@example.invalid" || got.TshirtSize != "L" || got.GetTermsAgreedAt() != original || got.SponsorProvideAgreedAt == nil {
		t.Fatalf("profile update changed consent: %+v", got)
	}
	registrationRequest(t, p, client, "PUT", "/api/registration/record", &pb.RegistrationRecordInput{AgreeSponsor: pointer(false), TshirtSize: pointer("invalid")}, true, 422).Body.Close()
	testkit.DecodePB(t, registrationRequest(t, p, client, "GET", "/api/registration/record", nil, false, 200), got)
	if got.SponsorProvideAgreedAt == nil {
		t.Fatal("invalid update partially saved consent")
	}

	team, err := model.FindTeam(context.Background(), p.DB, created.TeamId)
	if err != nil {
		t.Fatal(err)
	}
	member := registrationClient(t, p, &session.Session{GitHubLogin: &session.OAuthIdentity{ID: "1002", Login: "bob"}, DiscordLogin: &session.OAuthIdentity{ID: "2002", Tag: "bob"}})
	registrationRequest(t, p, member, "POST", "/api/registration/contestant", &pb.JoinTeamRequest{TeamId: team.ID, InviteToken: team.InviteToken, Name: "Bob", RegistrationRecord: &pb.RegistrationRecordInput{AgreeTerms: pointer(true), AgreePrivacy: pointer(true)}}, true, 200).Body.Close()
	memberRecord := &pb.RegistrationRecord{}
	testkit.DecodePB(t, registrationRequest(t, p, member, "GET", "/api/registration/record", nil, false, 200), memberRecord)
	if memberRecord.TermsAgreedAt == nil || memberRecord.SponsorProvideAgreedAt != nil || memberRecord.MinorParentalConsent != nil {
		t.Fatalf("join manufactured consent: %+v", memberRecord)
	}
	// No access to somebody else's record; staff access is explicit and responses are uncached.
	adminPath := fmt.Sprintf("/api/admin/contestants/%d/registration_record", team.LeaderID.Int64)
	registrationRequest(t, p, member, "GET", adminPath, nil, false, 302).Body.Close()
	staff := registrationClient(t, p, &session.Session{Staff: true})
	res := registrationRequest(t, p, staff, "GET", adminPath, nil, false, 200)
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private record may be cached")
	}
	res.Body.Close()
	registrationRequest(t, p, client, "PUT", "/api/registration/record", &pb.RegistrationRecordInput{AgreeSponsor: pointer(false)}, false, 422).Body.Close()
	// Consent withdrawal/reagreement works after the contest and registration deadlines.
	p.Cfg.Timing.ContestStart = pointer(now.Add(-time.Hour))
	p.Cfg.Timing.RegistrationUpdateClose = pointer(now.Add(-time.Hour))
	testkit.DecodePB(t, registrationRequest(t, p, client, "PUT", "/api/registration/record", &pb.RegistrationRecordInput{AgreeSponsor: pointer(false)}, true, 200), got)
	if got.SponsorProvideAgreedAt != nil || got.SponsorProvideWithdrawnAt == nil || got.GetTermsAgreedAt() != original {
		t.Fatalf("withdrawal=%+v", got)
	}
	withdrawn := got.GetSponsorProvideWithdrawnAt()
	testkit.DecodePB(t, registrationRequest(t, p, client, "PUT", "/api/registration/record", &pb.RegistrationRecordInput{AgreeSponsor: pointer(false)}, true, 200), got)
	if got.GetSponsorProvideWithdrawnAt() != withdrawn {
		t.Fatal("repeat withdrawal changed time")
	}
	testkit.DecodePB(t, registrationRequest(t, p, client, "PUT", "/api/registration/record", &pb.RegistrationRecordInput{AgreeSponsor: pointer(true)}, true, 200), got)
	if got.SponsorProvideAgreedAt == nil || got.SponsorProvideWithdrawnAt != nil {
		t.Fatalf("reagreement=%+v", got)
	}
	// Withdrawal of team membership must not erase consent evidence.
	p.Cfg.Timing.ContestStart = nil
	p.Cfg.Timing.RegistrationUpdateClose = nil
	registrationRequest(t, p, client, "DELETE", "/api/registration", nil, true, 200).Body.Close()
	registrationRequest(t, p, staff, "GET", adminPath, nil, false, 200).Body.Close()
	var retainedGitHubID string
	if err := p.DB.QueryRow(`SELECT github_id FROM registration_records WHERE contestant_id=$1`, team.LeaderID.Int64).Scan(&retainedGitHubID); err != nil || retainedGitHubID != "1001" {
		t.Fatalf("withdrawal lost consent identity: %q %v", retainedGitHubID, err)
	}
}

func TestImportedRegistrationOAuthAndDiscordLink(t *testing.T) {
	p := testkit.Start(t)
	p.Cfg.TestMode = false
	p.Jobs.SetQueue(discardJobs{})
	s := registrationSnapshot()
	if err := s.Import(context.Background(), p.DB); err != nil {
		t.Fatal(err)
	}
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"local-token"}`)
		case "/user":
			fmt.Fprint(w, `{"id":123,"login":"alice"}`)
		case "/users/@me":
			fmt.Fprint(w, `{"id":"5001","username":"alice","discriminator":"0"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()
	p.Cfg.GitHub.AuthorizeURL = oauth.URL + "/authorize"
	p.Cfg.GitHub.TokenURL = oauth.URL + "/token"
	p.Cfg.GitHub.APIBaseURL = oauth.URL
	p.Cfg.Discord.AuthorizeURL = oauth.URL + "/authorize"
	p.Cfg.Discord.TokenURL = oauth.URL + "/token"
	p.Cfg.Discord.OAuthProvider.APIBaseURL = oauth.URL
	client := registrationClient(t, p, &session.Session{})
	for _, provider := range []string{"github", "discord"} {
		res := registrationRequest(t, p, client, "GET", "/auth/"+provider, nil, false, 302)
		authorize, err := url.Parse(res.Header.Get("Location"))
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if authorize.Query().Get("redirect_uri") != p.Server.URL+"/auth/"+provider+"/callback" {
			t.Fatal("wrong redirect URI")
		}
		callback := "/auth/" + provider + "/callback?" + url.Values{"state": {authorize.Query().Get("state")}, "code": {"test"}}.Encode()
		callbackRes := registrationRequest(t, p, client, "GET", callback, nil, false, 302)
		if callbackRes.Header.Get("Location") == "/auth/failure" {
			t.Fatal("OAuth callback failed")
		}
		callbackRes.Body.Close()
		state := &pb.GetRegistrationSessionResponse{}
		testkit.DecodePB(t, registrationRequest(t, p, client, "GET", "/api/registration/session", nil, false, 200), state)
		if state.Status != pb.GetRegistrationSessionResponse_JOINED || state.Team.Id != 20 {
			t.Fatalf("imported login=%+v", state)
		}
	}
	c, err := model.FindContestant(context.Background(), p.DB, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscordID != "5001" {
		t.Fatalf("Discord not linked: %+v", c)
	}
	record := &pb.RegistrationRecord{}
	testkit.DecodePB(t, registrationRequest(t, p, client, "GET", "/api/registration/record", nil, false, 200), record)
	if record.GetTshirtSize() != "XL" || record.SponsorProvideWithdrawnAt == nil {
		t.Fatalf("imported record=%+v", record)
	}
	registrationRequest(t, p, client, "GET", "/api/admin/registration_source/10?model=authentication.User", nil, false, 302).Body.Close()
	staff := registrationClient(t, p, &session.Session{Staff: true})
	registrationRequest(t, p, staff, "GET", "/api/admin/registration_source/10?model=authentication.User", nil, false, 200).Body.Close()
}

func TestOAuthMissingIdentityCannotSelectUnlinkedContestant(t *testing.T) {
	p := testkit.Start(t)
	p.Cfg.TestMode = false
	p.Jobs.SetQueue(discardJobs{})
	s := registrationSnapshot()
	if err := s.Import(context.Background(), p.DB); err != nil {
		t.Fatal(err)
	}
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"local-token"}`)
		} else {
			fmt.Fprint(w, `{}`)
		}
	}))
	defer oauth.Close()
	p.Cfg.Discord.AuthorizeURL = oauth.URL + "/authorize"
	p.Cfg.Discord.TokenURL = oauth.URL + "/token"
	p.Cfg.Discord.OAuthProvider.APIBaseURL = oauth.URL
	client := registrationClient(t, p, &session.Session{})
	res := registrationRequest(t, p, client, "GET", "/auth/discord", nil, false, 302)
	authorize, _ := url.Parse(res.Header.Get("Location"))
	res.Body.Close()
	callback := "/auth/discord/callback?" + url.Values{"state": {authorize.Query().Get("state")}, "code": {"test"}}.Encode()
	res = registrationRequest(t, p, client, "GET", callback, nil, false, 302)
	defer res.Body.Close()
	if res.Header.Get("Location") != "/auth/failure" {
		t.Fatal("empty Discord identity selected an unlinked contestant")
	}
}
