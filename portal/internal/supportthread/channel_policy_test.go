package supportthread

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestChannelPolicyRepairsDriftAndIsIdempotent(t *testing.T) {
	channels := map[string]*discordgo.Channel{
		"parent": {ID: "parent", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, Name: "wrong-parent"},
		"commands": {ID: "commands", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, Name: "support-commands", PermissionOverwrites: []*discordgo.PermissionOverwrite{
			{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel},
			{ID: "guest", Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionAttachFiles},
		}},
	}
	writes := 0
	s := fakeSession(t, func(r *http.Request) string {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		ch := channels[id]
		if ch == nil {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		if r.Method == "PATCH" {
			var edit discordgo.ChannelEdit
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Fatal(err)
			}
			if writes == 6 && edit.Name != "" {
				t.Fatal("permission-only repair unnecessarily renames channel")
			}
			if edit.Name != "" {
				if channels["parent"].PermissionOverwrites == nil {
					t.Fatal("rename attempted before both channel permissions were repaired")
				}
				ch.Name = edit.Name
			}
			if edit.PermissionOverwrites != nil {
				ch.PermissionOverwrites = edit.PermissionOverwrites
			}
			writes++
		}
		raw, _ := json.Marshal(ch)
		return string(raw)
	})
	past := time.Unix(0, 0)
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", CommandChannelID: "commands", AdminRoleID: "ops", ContestStart: &past}
	if err := d.ReconcileChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes != 4 || channels["commands"].Name != "admin-commands" || channels["parent"].Name != "support-threads" {
		t.Fatalf("channels not repaired: %d", writes)
	}
	find := func(channel, id string) *discordgo.PermissionOverwrite {
		for _, o := range channels[channel].PermissionOverwrites {
			if o.ID == id {
				return o
			}
		}
		t.Fatalf("missing overwrite %s %s", channel, id)
		return nil
	}
	if find("commands", "guild").Deny&discordgo.PermissionViewChannel == 0 {
		t.Fatal("commands visible to participants")
	}
	guest := find("commands", "guest")
	if guest.Allow&discordgo.PermissionViewChannel != 0 || guest.Allow&discordgo.PermissionAttachFiles == 0 {
		t.Fatal("unexpected grant survived or unrelated bit lost")
	}
	if find("parent", "guild").Allow&discordgo.PermissionSendMessagesInThreads == 0 || find("parent", "guild").Deny&discordgo.PermissionSendMessages == 0 {
		t.Fatal("participant parent permissions incorrect")
	}
	if find("commands", "bot").Allow&(discordgo.PermissionManageRoles|discordgo.PermissionManageChannels) != 0 {
		t.Fatal("management permissions must be inherited, not granted to the bot itself")
	}
	if err := d.ReconcileChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes != 4 {
		t.Fatal("unchanged channels were rewritten")
	}
	channels["commands"].Name = "accidentally-renamed"
	channels["commands"].PermissionOverwrites = append(channels["commands"].PermissionOverwrites, &discordgo.PermissionOverwrite{ID: "other-role", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel})
	if err := d.ReconcileChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes != 6 {
		t.Fatal("later drift not repaired")
	}
	channels["commands"].PermissionOverwrites = append(channels["commands"].PermissionOverwrites, &discordgo.PermissionOverwrite{ID: "another-role", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel})
	if err := d.ReconcileChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writes != 7 {
		t.Fatal("permission-only drift not repaired")
	}
}

func TestChannelPolicyValidatesBothChannelsBeforeWriting(t *testing.T) {
	s := fakeSession(t, func(r *http.Request) string {
		if r.Method != "GET" {
			t.Fatal("wrote before validating channel types")
		}
		if strings.HasSuffix(r.URL.Path, "/parent") {
			return `{"id":"parent","guild_id":"guild","type":0}`
		}
		return `{"id":"commands","guild_id":"guild","type":2}`
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", CommandChannelID: "commands", AdminRoleID: "ops"}
	if err := d.ReconcileChannels(context.Background()); err == nil {
		t.Fatal("non-text command channel accepted")
	}
}

func TestChannelPolicyReturnsRepairFailure(t *testing.T) {
	s, _ := discordgo.New("Bot test")
	s.State.User = &discordgo.User{ID: "bot"}
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := `{"id":"parent","guild_id":"guild","type":0}`
		if strings.HasSuffix(r.URL.Path, "/commands") {
			body = `{"id":"commands","guild_id":"guild","type":0}`
		}
		if r.Method == "PATCH" {
			status = 403
			body = `{"message":"Missing Permissions","code":50013}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", CommandChannelID: "commands", AdminRoleID: "ops"}
	if err := d.ReconcileChannels(context.Background()); err == nil {
		t.Fatal("failed repair was accepted")
	}
}

func TestChannelPolicyContestOpening(t *testing.T) {
	start := time.Date(2026, 10, 31, 10, 0, 0, 0, time.FixedZone("JST", 9*3600))
	now := start.Add(-time.Nanosecond)
	channels := map[string]*discordgo.Channel{
		"parent":   {ID: "parent", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, Name: "support-threads"},
		"commands": {ID: "commands", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, Name: "admin-commands"},
	}
	s := fakeSession(t, func(r *http.Request) string {
		ch := channels[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
		if ch == nil {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		if r.Method == "PATCH" {
			var edit discordgo.ChannelEdit
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Fatal(err)
			}
			ch.PermissionOverwrites = edit.PermissionOverwrites
		}
		raw, _ := json.Marshal(ch)
		return string(raw)
	})
	d := DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", CommandChannelID: "commands", AdminRoleID: "ops", ContestStart: &start, now: func() time.Time { return now }}
	check := func(open bool) {
		t.Helper()
		if err := d.ReconcileChannels(context.Background()); err != nil {
			t.Fatal(err)
		}
		for name, ch := range channels {
			for _, o := range ch.PermissionOverwrites {
				visible := o.Allow&discordgo.PermissionViewChannel != 0
				if o.ID == "guild" {
					want := name == "parent" && open
					if visible != want || (!want && o.Deny&discordgo.PermissionViewChannel == 0) {
						t.Fatalf("%s public visibility=%v want=%v", name, visible, want)
					}
				}
				if (o.ID == "ops" || o.ID == "bot") && !visible {
					t.Fatalf("%s staff access lost", name)
				}
			}
		}
	}
	check(false)
	now = start.UTC()
	check(true)
	now = start.Add(24 * time.Hour)
	check(true)
	// Restarted bots derive visibility from the current clock.
	d = DiscordAPI{Session: s, GuildID: "guild", ParentID: "parent", CommandChannelID: "commands", AdminRoleID: "ops", ContestStart: &start, now: func() time.Time { return start.Add(time.Hour) }}
	check(true)
	d.ContestStart = nil
	check(false)
}
