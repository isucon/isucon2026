package config

import "testing"

func TestSupportThreadActivationRequiresExplicitTime(t *testing.T) {
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_PARENT_ID", "test-parent")
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_SINCE", "")
	if _, err := Load(); err == nil {
		t.Fatal("parent without activation boundary accepted")
	}
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_SINCE", "2026-10-20T00:00:00+09:00")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Discord.SupportThreadSince == nil {
		t.Fatal("activation time missing")
	}
}
