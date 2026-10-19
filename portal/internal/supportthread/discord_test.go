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
func TestParticipantCannotHaveThreadManagement(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		if strings.HasSuffix(r.URL.Path, "/members/member") {
			return `{"roles":["participant"]}`
		}
		if strings.HasSuffix(r.URL.Path, "/roles") {
			return fmt.Sprintf(`[{"id":"participant","permissions":"%d"}]`, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory|discordgo.PermissionSendMessagesInThreads|discordgo.PermissionManageThreads)
		}
		return `{"id":"parent","guild_id":"guild","type":0}`
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", AdminRoleID: "admin"}
	if err := d.CheckParticipant(context.Background(), "member"); err == nil {
		t.Fatal("participant could read closed threads")
	}
}
