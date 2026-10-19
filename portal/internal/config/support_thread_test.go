package config

import "testing"

func TestSupportThreadParentCannotBeLegacySupportChannel(t *testing.T) {
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_PARENT_ID", "test-parent")
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES", "test-parent=test-role")
	if _, err := Load(); err == nil {
		t.Fatal("ambiguous support command target accepted")
	}
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES", "")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}
