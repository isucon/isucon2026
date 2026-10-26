package supportthread_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/isucon/isucon2026/portal/internal/supportthread"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

type fakeDiscord struct {
	calls   []string
	members []string
	fail    string
	creates int
	staff   []string
	errors  map[string]error
}

func (f *fakeDiscord) call(s string) error {
	f.calls = append(f.calls, s)
	if err := f.errors[s]; err != nil {
		return err
	}
	if f.fail == s {
		return errors.New("injected failure")
	}
	return nil
}
func (f *fakeDiscord) EnsureThread(ctx context.Context, name string) (string, error) {
	if !strings.HasPrefix(name, "support-") {
		return "", fmt.Errorf("unexpected thread name: %s", name)
	}
	f.creates++
	id := "thread"
	if f.creates > 1 {
		id = fmt.Sprintf("thread-%d", f.creates)
	}
	return id, f.call("create")
}
func (f *fakeDiscord) CheckParticipant(ctx context.Context, id string) error {
	return f.call("check:" + id)
}
func (f *fakeDiscord) AddMember(ctx context.Context, thread, id string) error {
	if err := f.call("add:" + id); err != nil {
		return err
	}
	for _, m := range f.members {
		if m == id {
			return nil
		}
	}
	f.members = append(f.members, id)
	return nil
}
func (f *fakeDiscord) Lock(ctx context.Context, id string) error { return f.call("lock") }
func (f *fakeDiscord) Messages(ctx context.Context, id string) ([]*discordgo.Message, error) {
	return []*discordgo.Message{{ID: "message", Content: "private transcript"}}, f.call("messages")
}
func (f *fakeDiscord) Members(ctx context.Context, id string) ([]string, error) {
	return append([]string{}, f.members...), f.call("members")
}
func (f *fakeDiscord) RemoveMember(ctx context.Context, thread, id string) error {
	if err := f.call("remove:" + id); err != nil {
		return err
	}
	for i, m := range f.members {
		if m == id {
			f.members = append(f.members[:i], f.members[i+1:]...)
			break
		}
	}
	return nil
}
func (f *fakeDiscord) Archive(ctx context.Context, id string) error { return f.call("archive") }

func TestCloseRetriesWithoutExposingLogOrLosingMembers(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'test',90001,'member','test','test','test','',now(),now()); INSERT INTO clarifications(id,team_id,question,original_question,admin,created_at,updated_at) VALUES(90001,90001,'question','question',false,now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{}
	s := &supportthread.Service{DB: db, Discord: f}
	if _, err = s.Open(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Open(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 {
		t.Fatal("duplicate thread")
	}
	f.members = append(f.members, "unexpected-guest")
	f.fail = "messages"
	if err = s.Close(ctx, "thread"); err == nil {
		t.Fatal("expected log fetch failure")
	}
	if len(f.members) != 2 {
		t.Fatal("removed members before saving log")
	}
	f.fail = "remove:unexpected-guest"
	if err = s.Close(ctx, "thread"); err == nil {
		t.Fatal("expected remove failure")
	}
	var status, log string
	if err = db.QueryRow(`SELECT status,transcript::text FROM discord_support_threads WHERE clarification_id=90001`).Scan(&status, &log); err != nil {
		t.Fatal(err)
	}
	if status == "closed" || log == "[]" {
		t.Fatal("must retain log and incomplete close state")
	}
	f.fail = ""
	if err = s.Close(ctx, "thread"); err != nil {
		t.Fatal(err)
	}
	if len(f.members) != 0 {
		t.Fatal("guest still has access")
	}
	if err = s.Close(ctx, "thread"); err != nil {
		t.Fatal(err)
	}
	var answer *string
	if err = db.QueryRow(`SELECT answer FROM clarifications WHERE id=90001`).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != nil {
		t.Fatal("transcript leaked into participant clar")
	}
}

func TestOpenFailureCanResumeAndRejectsAnnouncements(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'test',90001,'member','test','test','test','',now(),now()); INSERT INTO clarifications(id,team_id,admin,created_at,updated_at) VALUES(90001,90001,false,now(),now()),(90002,90001,true,now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{fail: "add:member"}
	s := &supportthread.Service{DB: db, Discord: f}
	if _, err = s.Open(ctx, 90002); err == nil {
		t.Fatal("announcement accepted")
	}
	if _, err = s.Open(ctx, 90001); err == nil {
		t.Fatal("expected invite failure")
	}
	f.fail = ""
	if _, err = s.Open(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || len(f.members) != 1 {
		t.Fatal("resume created duplicate or lost invite")
	}
}

func TestFailedTranscriptWriteDoesNotRemoveParticipants(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO clarifications(id,admin,created_at,updated_at) VALUES(90001,false,now(),now()); INSERT INTO discord_support_threads(clarification_id,thread_id,participant_ids,status) VALUES(90001,'thread','["member"]','open'); CREATE FUNCTION reject_support_log() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.transcript_saved_at IS NOT NULL THEN RAISE EXCEPTION 'test failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_support_log BEFORE UPDATE ON discord_support_threads FOR EACH ROW EXECUTE FUNCTION reject_support_log()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER reject_support_log ON discord_support_threads; DROP FUNCTION reject_support_log()`)
	})
	f := &fakeDiscord{members: []string{"member"}}
	s := &supportthread.Service{DB: db, Discord: f}
	if err = s.Close(ctx, "thread"); err == nil {
		t.Fatal("expected write failure")
	}
	if len(f.members) != 1 {
		t.Fatal("participant removed before durable log")
	}
	for _, call := range f.calls {
		if call == "archive" {
			t.Fatal("archived after failed log write")
		}
	}
}

func TestRetriesNeverOpenNewClarificationsButResumeCommands(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'test',90001,'member','test','test','test','',now(),now()); INSERT INTO clarifications(id,team_id,admin,created_at,updated_at) VALUES(90001,90001,false,now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{fail: "add:member"}
	s := &supportthread.Service{DB: db, Discord: f}

	if err = s.RetryPending(ctx); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 {
		t.Fatal("backlog automatically opened")
	}
	if _, err = s.Open(ctx, 90001); err == nil {
		t.Fatal("expected invite failure")
	}
	f.fail = ""
	if err = s.RetryPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.members) != 1 {
		t.Fatal("manual opening not resumed")
	}
}

func TestTeamCommandReusesActiveConversationAndCreatesNewAfterClose(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'test',90001,'member','test','test','test','',now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{}
	s := &supportthread.Service{DB: db, Discord: f}
	first, err := s.OpenTeam(ctx, 90001)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.OpenTeam(ctx, 90001)
	if err != nil || first != again || f.creates != 1 {
		t.Fatalf("duplicate team conversation: %s %v", again, err)
	}
	if err = s.CloseTeam(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenTeam(ctx, 90001)
	if err != nil || second == first {
		t.Fatalf("new conversation not created: %s %v", second, err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM discord_support_threads`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old log not retained: %d %v", n, err)
	}
}

func (f *fakeDiscord) StaffIDs(context.Context) ([]string, error) { return f.staff, nil }
func TestStaffAreInvitedOnOpenAndRetainedOnClose(t *testing.T) {
	db := testkit.Database(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'test',90001,'member','test','test','test','',now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{staff: []string{"operator"}}
	s := &supportthread.Service{DB: db, Discord: f}
	thread, err := s.OpenTeam(ctx, 90001)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.members, "operator") {
		t.Fatal("operator was not invited")
	}
	f.staff = append(f.staff, "administrator")
	if _, err = s.OpenTeam(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.members, "administrator") {
		t.Fatal("new administrator was not invited on reuse")
	}
	if err = s.CloseTeam(ctx, 90001); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.members, "member") || len(f.members) != 2 {
		t.Fatalf("unexpected remaining membership: %v", f.members)
	}
	if _, err = s.Open(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || thread != "thread" {
		t.Fatal("closed thread recreated")
	}
}

func TestOpenInvitesOnlyCurrentGuildMembers(t *testing.T) {
	missing := &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusNotFound}, Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMember}}
	for _, tc := range []struct {
		name      string
		failures  map[string]error
		pending   bool
		wantError bool
	}{
		{name: "absent member", failures: map[string]error{"check:absent": missing}},
		{name: "resume persisted candidates", failures: map[string]error{"check:absent": missing}, pending: true},
		{name: "leaves before invitation", failures: map[string]error{"add:absent": missing}},
		{name: "all absent then joins", failures: map[string]error{"check:absent": missing, "check:present": missing}, wantError: true},
		{name: "permission failure", failures: map[string]error{"check:absent": &discordgo.RESTError{Response: &http.Response{StatusCode: 403}, Message: &discordgo.APIErrorMessage{Code: 50013}}}, wantError: true},
		{name: "unrelated not found", failures: map[string]error{"check:absent": &discordgo.RESTError{Response: &http.Response{StatusCode: 404}, Message: &discordgo.APIErrorMessage{Code: 10003}}}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testkit.Database(t)
			_, err := db.Exec(`INSERT INTO teams(id,name,email_address,invite_token,student,created_at,updated_at) VALUES(90001,'test','test@example.invalid','token',false,now(),now()); INSERT INTO contestants(id,name,team_id,discord_id,github_login,github_id,discord_tag,avatar_url,created_at,updated_at) VALUES(90001,'absent',90001,'absent','absent','absent','absent','',now(),now()),(90002,'present',90001,'present','present','present','present','',now(),now()); INSERT INTO clarifications(id,team_id,admin,created_at,updated_at) VALUES(90001,90001,true,now(),now()); INSERT INTO discord_support_threads(clarification_id,participant_ids,status) VALUES(90001,'[]','opening')`)
			if err != nil {
				t.Fatal(err)
			}
			if tc.pending {
				if _, err = db.Exec(`UPDATE discord_support_threads SET participant_ids='["absent","present"]'`); err != nil {
					t.Fatal(err)
				}
			}
			f := &fakeDiscord{errors: tc.failures, staff: []string{"staff"}}
			s := &supportthread.Service{DB: db, Discord: f}
			_, err = s.Open(context.Background(), 90001)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				if f.creates != 0 {
					t.Fatal("created a thread without eligible participants")
				}
				if tc.name != "all absent then joins" {
					return
				}
				delete(f.errors, "check:present")
				if err = s.RetryPending(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(f.members, "present") || !slices.Contains(f.members, "staff") || slices.Contains(f.members, "absent") {
				t.Fatalf("invited members: %v", f.members)
			}
			var status string
			var raw []byte
			if err = db.QueryRow(`SELECT status,participant_ids FROM discord_support_threads WHERE clarification_id=90001`).Scan(&status, &raw); err != nil {
				t.Fatal(err)
			}
			var ids []string
			if err = json.Unmarshal(raw, &ids); err != nil {
				t.Fatal(err)
			}
			if status != "open" || !slices.Equal(ids, []string{"present"}) {
				t.Fatalf("status=%s participants=%v", status, ids)
			}
			if _, err = s.OpenTeam(context.Background(), 90001); err != nil {
				t.Fatal(err)
			}
			if f.creates != 1 {
				t.Fatalf("created %d threads", f.creates)
			}
		})
	}
}
