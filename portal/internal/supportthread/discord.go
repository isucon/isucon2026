package supportthread

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type DiscordAPI struct {
	Session                        *discordgo.Session
	GuildID, ParentID, AdminRoleID string
}

func (d *DiscordAPI) botID() string { return d.Session.State.User.ID }
func (d *DiscordAPI) permissions(ctx context.Context, uid string) (int64, bool, error) {
	member, err := d.Session.GuildMember(d.GuildID, uid, discordgo.WithContext(ctx))
	if err != nil {
		return 0, false, err
	}
	roles, err := d.Session.GuildRoles(d.GuildID, discordgo.WithContext(ctx))
	if err != nil {
		return 0, false, err
	}
	ch, err := d.Session.Channel(d.ParentID, discordgo.WithContext(ctx))
	if err != nil {
		return 0, false, err
	}
	if ch.Type != discordgo.ChannelTypeGuildText || ch.GuildID != d.GuildID {
		return 0, false, errors.New("support thread parent must be a text channel in the configured guild")
	}
	selected := map[string]bool{d.GuildID: true}
	staff := false
	for _, r := range member.Roles {
		selected[r] = true
		if r == d.AdminRoleID {
			staff = true
		}
	}
	var p int64
	for _, r := range roles {
		if selected[r.ID] {
			p |= r.Permissions
		}
	}
	if p&discordgo.PermissionAdministrator != 0 {
		return ^int64(0), staff, nil
	}
	for _, o := range ch.PermissionOverwrites {
		if o.ID == d.GuildID {
			p = (p &^ o.Deny) | o.Allow
		}
	}
	var allow, deny int64
	for _, o := range ch.PermissionOverwrites {
		if o.Type == discordgo.PermissionOverwriteTypeRole && o.ID != d.GuildID && selected[o.ID] {
			allow |= o.Allow
			deny |= o.Deny
		}
	}
	p = (p &^ deny) | allow
	for _, o := range ch.PermissionOverwrites {
		if o.Type == discordgo.PermissionOverwriteTypeMember && o.ID == uid {
			p = (p &^ o.Deny) | o.Allow
		}
	}
	return p, staff, nil
}
func (d *DiscordAPI) CheckParticipant(ctx context.Context, uid string) error {
	p, staff, err := d.permissions(ctx, uid)
	if err != nil {
		return err
	}
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionSendMessagesInThreads)
	if p&required != required {
		return fmt.Errorf("member %s lacks parent view/history/thread-send permissions", uid)
	}
	if !staff && p&(discordgo.PermissionManageThreads|discordgo.PermissionAdministrator) != 0 {
		return fmt.Errorf("participant %s can bypass private thread membership", uid)
	}
	return nil
}
func (d *DiscordAPI) EnsureThread(ctx context.Context, name string) (string, error) {
	p, _, err := d.permissions(ctx, d.botID())
	if err != nil {
		return "", err
	}
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionCreatePrivateThreads | discordgo.PermissionManageThreads)
	if p&required != required {
		return "", errors.New("Bot lacks private thread permissions")
	}
	// Recover a thread created before a database write failed, without inviting
	// anyone until the association has been durably stored.
	active, err := d.Session.GuildThreadsActive(d.GuildID, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	legacyName := ""
	if strings.HasPrefix(name, "support-") {
		legacyName = "clar-" + strings.TrimPrefix(name, "support-")
	}
	matches := []*discordgo.Channel{}
	collect := func(chs []*discordgo.Channel) {
		for _, ch := range chs {
			if ch.ParentID == d.ParentID && (ch.Name == name || (legacyName != "" && ch.Name == legacyName)) && ch.OwnerID == d.botID() {
				matches = append(matches, ch)
			}
		}
	}
	collect(active.Threads)
	var before *time.Time
	for {
		page, e := d.Session.ThreadsPrivateArchived(d.ParentID, before, 100, discordgo.WithContext(ctx))
		if e != nil {
			return "", e
		}
		collect(page.Threads)
		if !page.HasMore || len(page.Threads) == 0 {
			break
		}
		t := page.Threads[len(page.Threads)-1].ThreadMetadata.ArchiveTimestamp
		before = &t
	}
	if len(matches) > 1 {
		return "", errors.New("multiple matching support threads require operator review")
	}
	if len(matches) == 1 {
		ch := matches[0]
		if ch.Type != discordgo.ChannelTypeGuildPrivateThread {
			return "", errors.New("matching support thread is not private")
		}
		no := false
		if _, err = d.Session.ChannelEditComplex(ch.ID, &discordgo.ChannelEdit{Name: name, Archived: &no, Invitable: &no}, discordgo.WithContext(ctx)); err != nil {
			return "", err
		}
		return ch.ID, nil
	}
	ch, err := d.Session.ThreadStartComplex(d.ParentID, &discordgo.ThreadStart{Name: name, Type: discordgo.ChannelTypeGuildPrivateThread, Invitable: false, AutoArchiveDuration: 1440}, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return ch.ID, nil
}
func (d *DiscordAPI) AddMember(ctx context.Context, thread, uid string) error {
	return d.Session.ThreadMemberAdd(thread, uid, discordgo.WithContext(ctx))
}
func (d *DiscordAPI) Lock(ctx context.Context, id string) error {
	yes, no := true, false
	_, err := d.Session.ChannelEditComplex(id, &discordgo.ChannelEdit{Locked: &yes, Archived: &no, Invitable: &no}, discordgo.WithContext(ctx))
	return err
}
func (d *DiscordAPI) Archive(ctx context.Context, id string) error {
	yes, no := true, false
	_, err := d.Session.ChannelEditComplex(id, &discordgo.ChannelEdit{Archived: &yes, Locked: &yes, Invitable: &no}, discordgo.WithContext(ctx))
	return err
}
func (d *DiscordAPI) Messages(ctx context.Context, id string) ([]*discordgo.Message, error) {
	out := []*discordgo.Message{}
	before := ""
	for {
		msgs, err := d.Session.ChannelMessages(id, 100, before, "", "", discordgo.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		out = append(out, msgs...)
		next := msgs[len(msgs)-1].ID
		if next == before {
			return nil, errors.New("message pagination did not advance")
		}
		before = next
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.ParseUint(out[i].ID, 10, 64)
		b, _ := strconv.ParseUint(out[j].ID, 10, 64)
		return a < b
	})
	return out, nil
}
func (d *DiscordAPI) Members(ctx context.Context, id string) ([]string, error) {
	var out []string
	after := ""
	for {
		members, err := d.Session.ThreadMembers(id, 100, false, after, discordgo.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.UserID != d.botID() {
				out = append(out, m.UserID)
			}
		}
		if len(members) < 100 {
			break
		}
		next := members[len(members)-1].UserID
		if next == after {
			return nil, errors.New("member pagination did not advance")
		}
		after = next
	}
	return out, nil
}
func (d *DiscordAPI) RemoveMember(ctx context.Context, thread, uid string) error {
	return d.Session.ThreadMemberRemove(thread, uid, discordgo.WithContext(ctx))
}
