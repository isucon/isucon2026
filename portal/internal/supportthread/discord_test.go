package supportthread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeSession(t *testing.T, respond func(*http.Request) string) *discordgo.Session {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	s.State.User = &discordgo.User{ID: "bot"}
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(respond(r))), Request: r}, nil
	})}
	return s
}
func TestEnsureThreadCreatesPrivateNonInvitableThread(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		switch {
		case strings.HasSuffix(r.URL.Path, "/members/bot"):
			return `{"roles":["botrole"]}`
		case strings.HasSuffix(r.URL.Path, "/roles"):
			return fmt.Sprintf(`[{"id":"botrole","permissions":"%d"}]`, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory|discordgo.PermissionSendMessagesInThreads|discordgo.PermissionCreatePrivateThreads|discordgo.PermissionManageThreads)
		case strings.HasSuffix(r.URL.Path, "/channels/parent"):
			return `{"id":"parent","guild_id":"guild","type":0}`
		case strings.HasSuffix(r.URL.Path, "/threads/active"), strings.HasSuffix(r.URL.Path, "/threads/archived/private"):
			return `{"threads":[],"has_more":false}`
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/threads"):
			var v discordgo.ThreadStart
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Fatal(err)
			}
			if v.Type != discordgo.ChannelTypeGuildPrivateThread || v.Invitable {
				t.Fatal("thread must be private and non-invitable")
			}
			return `{"id":"thread","type":12}`
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return ""
		}
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent"}
	if id, err := d.EnsureThread(context.Background(), "clar-1"); err != nil || id != "thread" {
		t.Fatalf("%s %v", id, err)
	}
}
func TestMessagesPaginatesBackwardsAndPreservesAttachments(t *testing.T) {
	requests := 0
	s := fakeSession(t, func(r *http.Request) string {
		requests++
		before := r.URL.Query().Get("before")
		if requests == 1 && before != "" {
			t.Fatal("first page cursor")
		}
		if requests == 2 {
			if before != "101" {
				t.Fatalf("cursor %s", before)
			}
			return `[{"id":"100","content":"old","attachments":[{"id":"a","filename":"log.txt"}]}]`
		}
		if requests == 3 {
			if before != "100" {
				t.Fatal("last cursor")
			}
			return `[]`
		}
		msgs := []*discordgo.Message{}
		for i := 200; i >= 101; i-- {
			msgs = append(msgs, &discordgo.Message{ID: fmt.Sprint(i)})
		}
		raw, _ := json.Marshal(msgs)
		return string(raw)
	})
	d := DiscordAPI{Session: s}
	msgs, err := d.Messages(context.Background(), "thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 101 || msgs[0].ID != "100" || len(msgs[0].Attachments) != 1 || msgs[100].ID != "200" {
		t.Fatal("incomplete or unordered log")
	}
}
func TestParticipantWithThreadManagementCanBeInvited(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		if strings.HasSuffix(r.URL.Path, "/members/member") {
			return `{"roles":["participant"]}`
		}
		if strings.HasSuffix(r.URL.Path, "/roles") {
			return fmt.Sprintf(`[{"id":"participant","permissions":"%d"}]`, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory|discordgo.PermissionSendMessagesInThreads|discordgo.PermissionManageThreads)
		}
		return `{"id":"parent","guild_id":"guild","type":0}`
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent"}
	if err := d.CheckParticipant(context.Background(), "member"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureThreadRecoversLegacyName(t *testing.T) {
	renamed := false
	s := fakeSession(t, func(r *http.Request) string {
		switch {
		case strings.HasSuffix(r.URL.Path, "/members/bot"):
			return `{"roles":["botrole"]}`
		case strings.HasSuffix(r.URL.Path, "/roles"):
			return `[{"id":"botrole","permissions":"8"}]`
		case strings.HasSuffix(r.URL.Path, "/channels/parent"):
			return `{"id":"parent","guild_id":"guild","type":0}`
		case strings.HasSuffix(r.URL.Path, "/threads/active"):
			return `{"threads":[{"id":"existing","parent_id":"parent","owner_id":"bot","name":"clar-1","type":12}]}`
		case strings.HasSuffix(r.URL.Path, "/threads/archived/private"):
			return `{"threads":[],"has_more":false}`
		case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/channels/existing"):
			var edit discordgo.ChannelEdit
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Fatal(err)
			}
			if edit.Name != "support-1" {
				t.Fatalf("unexpected name %q", edit.Name)
			}
			renamed = true
			return `{"id":"existing","type":12}`
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			return `{}`
		}
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent"}
	id, err := d.EnsureThread(context.Background(), "support-1")
	if err != nil || id != "existing" || !renamed {
		t.Fatalf("legacy thread not recovered: %s %v", id, err)
	}
}

func TestDiscordAdministratorCanBeInvited(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		switch {
		case strings.HasSuffix(r.URL.Path, "/members/admin"):
			return `{"roles":["administrator"]}`
		case strings.HasSuffix(r.URL.Path, "/roles"):
			return `[{"id":"administrator","permissions":"8"}]`
		case strings.HasSuffix(r.URL.Path, "/channels/parent"):
			return `{"id":"parent","guild_id":"guild","type":0}`
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
			return ""
		}
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent"}
	if err := d.CheckParticipant(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestStaffIDsIncludesOperatorsAdministratorsAndOwnerAcrossPages(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		switch {
		case strings.HasSuffix(r.URL.Path, "/roles"):
			return `[{"id":"ops","permissions":"0"},{"id":"admin","permissions":"8"}]`
		case strings.HasSuffix(r.URL.Path, "/members"):
			if r.URL.Query().Get("after") != "" {
				return `[{"user":{"id":"owner"},"roles":[]},{"user":{"id":"bot"},"roles":["admin"]}]`
			}
			page := make([]*discordgo.Member, 1000)
			for i := range page {
				page[i] = &discordgo.Member{User: &discordgo.User{ID: fmt.Sprint(i + 1)}}
			}
			page[0].Roles = []string{"ops"}
			page[1].Roles = []string{"admin"}
			raw, _ := json.Marshal(page)
			return string(raw)
		case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			return `{"id":"guild","owner_id":"owner"}`
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
			return ""
		}
	})
	d := DiscordAPI{Session: s, GuildID: "guild", AdminRoleID: "ops"}
	ids, err := d.StaffIDs(context.Background())
	if err != nil || strings.Join(ids, ",") != "1,2,owner" {
		t.Fatalf("unexpected staff: %v %v", ids, err)
	}
}
