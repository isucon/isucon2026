package isureal

import (
	"encoding/json"
	"testing"
)

func TestAbuseLogLine(t *testing.T) {
	for _, test := range []struct {
		action string
		status int
		kind   string
	}{
		{"put_profile_image", 400, "invalid_image"},
		{"get_image", 403, "invalid_signature"},
	} {
		var entry struct {
			Action string `json:"action"`
			Status int    `json:"status"`
			IP     string `json:"ip"`
			Abuse  string `json:"abuse"`
		}
		if err := json.Unmarshal(abuseLogLine("192.0.2.1", test.action, test.status, test.kind), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Action != test.action || entry.Status != test.status || entry.IP != "192.0.2.1" || entry.Abuse != test.kind {
			t.Fatalf("unexpected action log: %+v", entry)
		}
	}
}
