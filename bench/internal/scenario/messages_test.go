package scenario

import (
	"slices"
	"strings"
	"testing"
)

func TestMessagesSplitLines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		groups [][]string
		want   []string
	}{
		{name: "empty", want: []string{}},
		{name: "single line", reason: "failure", groups: [][]string{{"detail"}}, want: []string{"failure", "detail"}},
		{name: "multiline reason", reason: "failure: user A\nuser B\nuser C", want: []string{"failure: user A", "user B", "user C"}},
		{name: "multiline groups and duplicate reason", reason: "failure\ncontext", groups: [][]string{{"failure\ncontext", "critical A\ncritical B"}, {"error A\r\nerror B"}}, want: []string{"failure", "context", "critical A", "critical B", "error A", "error B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := messages(tc.reason, tc.groups...)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("messages = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMessagesLimitPreservesMultilineDetails(t *testing.T) {
	group := slices.Repeat([]string{"detail"}, maxMessages)
	got := messages("failure\ncontext", group)
	want := append([]string{"failure", "context"}, group[:maxMessages-1]...)
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	got = messages(strings.Repeat("detail\n", maxMessages) + "last detail")
	if len(got) != maxMessages+1 || got[len(got)-1] != "last detail" {
		t.Fatalf("multiline reason truncated: %q", got)
	}
}
