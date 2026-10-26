package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/isucon/isucon2026/portal/internal/app"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/supportthread"
	"strings"
	"testing"
)

func TestThreadCommandsDoNotReplyInPublicParent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Discord.SupportThreadParentID = "parent"
	b := &bot{app: &app.App{Cfg: cfg}}
	for _, command := range []string{".support", ".supportOpen", ".supportClose"} {
		e := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "parent"}}
		if !b.threadCommand(nil, e, []string{command, "90001"}, func(string) { t.Fatal("reply exposed in public parent") }) {
			t.Fatal("public command fell through to legacy handler")
		}
	}
}

func TestThreadCommandChannelRequiresOperatorRole(t *testing.T) {
	cfg := &config.Config{}
	cfg.Discord.SupportThreadCommandChannelID = "commands"
	cfg.Discord.AdminRoleID = "operator"
	b := &bot{app: &app.App{Cfg: cfg}}
	for _, tc := range []struct {
		roles []string
		want  string
	}{
		{[]string{}, "**FORBIDDEN**"}, {[]string{"operator"}, "**USAGE**"},
	} {
		e := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "commands", Member: &discordgo.Member{Roles: tc.roles}}}
		var response string
		if !b.threadCommand(nil, e, []string{".support"}, func(s string) { response = s }) {
			t.Fatal("command channel ignored")
		}
		if !strings.HasPrefix(response, tc.want) {
			t.Fatalf("unexpected response %q", response)
		}
	}
}

func TestThreadCommandDoesNotReplyWhenChannelCannotBeVerified(t *testing.T) {
	cfg := &config.Config{}
	cfg.Discord.SupportThreadCommandChannelID = "commands"
	b := &bot{app: &app.App{Cfg: cfg}, channelPolicy: &supportthread.DiscordAPI{}}
	e := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: "commands"}}
	if !b.threadCommand(nil, e, []string{".support"}, func(string) { t.Fatal("reply exposed before verifying permissions") }) {
		t.Fatal("unverified command fell through")
	}
}
