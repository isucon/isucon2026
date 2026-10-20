package supportthread

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/bwmarrin/discordgo"
)

const supportChannelPermissions int64 = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionCreatePublicThreads | discordgo.PermissionCreatePrivateThreads | discordgo.PermissionManageThreads

func (d *DiscordAPI) channelOverwrites(current []*discordgo.PermissionOverwrite, commands bool) []*discordgo.PermissionOverwrite {
	viewHistory := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	type key struct {
		id   string
		kind discordgo.PermissionOverwriteType
	}
	result := map[key]*discordgo.PermissionOverwrite{}
	for _, o := range current {
		copy := *o
		copy.Allow &^= supportChannelPermissions
		copy.Deny &^= supportChannelPermissions
		result[key{o.ID, o.Type}] = &copy
	}
	set := func(id string, kind discordgo.PermissionOverwriteType, allow, deny, mask int64) {
		k := key{id, kind}
		o := result[k]
		if o == nil {
			o = &discordgo.PermissionOverwrite{ID: id, Type: kind}
			result[k] = o
		}
		o.Allow = (o.Allow &^ mask) | allow
		o.Deny = (o.Deny &^ mask) | deny
	}
	everyoneAllow := viewHistory | discordgo.PermissionSendMessagesInThreads
	operatorAllow := everyoneAllow | discordgo.PermissionSendMessages | discordgo.PermissionManageThreads
	botAllow := operatorAllow | discordgo.PermissionCreatePrivateThreads
	if commands {
		everyoneAllow = 0
		operatorAllow = viewHistory | discordgo.PermissionSendMessages
		botAllow = operatorAllow
	}
	set(d.GuildID, discordgo.PermissionOverwriteTypeRole, everyoneAllow, supportChannelPermissions&^everyoneAllow, supportChannelPermissions)
	set(d.AdminRoleID, discordgo.PermissionOverwriteTypeRole, operatorAllow, supportChannelPermissions&^operatorAllow, supportChannelPermissions)
	// Management permissions come from the bot role. Discord can reject
	// granting ManageRoles to the bot itself even when it can edit overwrites.
	set(d.botID(), discordgo.PermissionOverwriteTypeMember, botAllow, supportChannelPermissions&^botAllow, supportChannelPermissions)
	out := []*discordgo.PermissionOverwrite{}
	for _, o := range result {
		if o.Allow != 0 || o.Deny != 0 {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func sameOverwrites(a, b []*discordgo.PermissionOverwrite) bool {
	if len(a) != len(b) {
		return false
	}
	for _, o := range a {
		if !slices.ContainsFunc(b, func(p *discordgo.PermissionOverwrite) bool { return *p == *o }) {
			return false
		}
	}
	return true
}

// ReconcileChannels repairs names and access rules. Unrelated permission bits
// are retained; extra grants for managed access rules are removed.
func (d *DiscordAPI) ReconcileChannels(ctx context.Context) error {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()
	if d.GuildID == "" || d.AdminRoleID == "" || d.AdminRoleID == d.GuildID || d.ParentID == "" || d.CommandChannelID == "" || d.ParentID == d.CommandChannelID {
		return errors.New("invalid support channel policy configuration")
	}
	if d.Session == nil || d.Session.State == nil || d.Session.State.User == nil {
		return errors.New("Discord session is not ready")
	}
	type target struct {
		id, name string
		commands bool
		channel  *discordgo.Channel
	}
	targets := []target{{id: d.CommandChannelID, name: "admin-commands", commands: true}, {id: d.ParentID, name: "support-threads"}}
	// Validate both destinations before making any changes.
	for i := range targets {
		ch, err := d.Session.Channel(targets[i].id, discordgo.WithContext(ctx))
		if err != nil {
			return err
		}
		if ch.GuildID != d.GuildID || ch.Type != discordgo.ChannelTypeGuildText {
			return errors.New("support channels must be text channels in the configured guild")
		}
		targets[i].channel = ch
	}
	for _, target := range targets {
		desired := d.channelOverwrites(target.channel.PermissionOverwrites, target.commands)
		if sameOverwrites(target.channel.PermissionOverwrites, desired) {
			continue
		}
		edit := &discordgo.ChannelEdit{PermissionOverwrites: desired}
		if _, err := d.Session.ChannelEditComplex(target.id, edit, discordgo.WithContext(ctx)); err != nil {
			return fmt.Errorf("repair %s: %w", target.name, err)
		}
		actual, err := d.Session.Channel(target.id, discordgo.WithContext(ctx))
		if err != nil {
			return err
		}
		if !sameOverwrites(actual.PermissionOverwrites, desired) {
			return fmt.Errorf("%s policy repair did not persist", target.name)
		}
	}
	// Repair both access policies before renaming: Discord's separate name
	// rate limit must not prevent the other channel's privacy repair.
	for _, target := range targets {
		if target.channel.Name == target.name {
			continue
		}
		actual, err := d.Session.ChannelEditComplex(target.id, &discordgo.ChannelEdit{Name: target.name}, discordgo.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("rename %s: %w", target.name, err)
		}
		if actual.Name != target.name {
			return fmt.Errorf("%s rename did not persist", target.name)
		}
	}
	return nil
}
