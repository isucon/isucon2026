package config

import "testing"

func TestSupportThreadParentCannotBeLegacySupportChannel(t *testing.T) {
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_PARENT_ID", "test-parent")
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_COMMAND_CHANNEL_ID", "test-commands")
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES", "test-parent=test-role")
	if _, err := Load(); err == nil {
		t.Fatal("ambiguous support command target accepted")
	}
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES", "")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestSupportThreadCommandsRequireSeparateChannel(t *testing.T) {
	t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_PARENT_ID", "test-parent")
	for _, id := range []string{"", "test-parent", "legacy"} {
		t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_THREAD_COMMAND_CHANNEL_ID", id)
		t.Setenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES", "legacy=role")
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe command channel %q accepted", id)
		}
	}
}
