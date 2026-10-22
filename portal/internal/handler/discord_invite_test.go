package handler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/config"
)

func TestDiscordInviteConfiguration(t *testing.T) {
	t.Setenv("ISUXPORTAL_DISCORD_INVITE_URL", "https://discord.gg/test-invite")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var html bytes.Buffer
	if err := shellTemplate.Execute(&html, shellData{DiscordInviteURL: cfg.Discord.InviteURL}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `<meta name="isux:discord-invite-url" content="https://discord.gg/test-invite">`) {
		t.Fatal("missing configured invite in shell")
	}
}
