package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// Match the JSON request sent by isucon14-portal/envcheck, including boolean passed.
func TestEnvCheckJSONResult(t *testing.T) {
	p := testkit.Start(t)
	teamID, _ := p.CreateTeam(t, "json-envcheck")
	for _, passed := range []bool{true, false} {
		payload := map[string]any{
			"token": checkerTokenParam(t, p, teamID), "name": "test-ssh",
			"passed": passed, "ip_address": "203.0.113.42",
			"message": "環境チェック", "admin_message": "AWS details", "raw_data": `{"Instances":[]}`,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Server.Client().Post(p.Server.URL+"/api/env_checks", "application/json; charset=utf-8", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if got := readAll(t, res); res.StatusCode != http.StatusNoContent || got != "" {
			t.Fatalf("JSON result = %d %q, want 204 empty", res.StatusCode, got)
		}
		var gotPassed bool
		var ip, message, adminMessage, raw string
		err = p.DB.QueryRow(`SELECT passed, ip_address, message, admin_message, raw_data FROM env_checks WHERE team_id = $1 AND name = 'test-ssh' ORDER BY id DESC LIMIT 1`, teamID).Scan(&gotPassed, &ip, &message, &adminMessage, &raw)
		if err != nil {
			t.Fatal(err)
		}
		if gotPassed != passed || ip != payload["ip_address"] || message != payload["message"] || adminMessage != payload["admin_message"] || raw != payload["raw_data"] {
			t.Fatalf("stored result differs: %v %q %q %q %q", gotPassed, ip, message, adminMessage, raw)
		}
	}
}

func TestEnvCheckInvalidJSON(t *testing.T) {
	p := testkit.Start(t)
	teamID, _ := p.CreateTeam(t, "invalid-envcheck")
	tok := checkerTokenParam(t, p, teamID)
	for _, body := range []string{`{`, `null`, `{"passed":"true"}`, `{} {}`} {
		res, err := p.Server.Client().Post(p.Server.URL+"/api/env_checks?token="+tok, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if got := readAll(t, res); res.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid JSON %q = %d %q, want 400", body, res.StatusCode, got)
		}
	}
	res, err := p.Server.Client().Post(p.Server.URL+"/api/env_checks", "application/json", bytes.NewBufferString(`{"token":"invalid","name":"test-ssh","passed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, res); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("invalid token = %d %q, want 401", res.StatusCode, got)
	}
	var count int
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM env_checks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("invalid requests stored %d results", count)
	}
}
