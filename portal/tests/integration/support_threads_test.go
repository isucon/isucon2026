package integration_test

import (
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/tests/testkit"
	"net/http"
	"strings"
	"testing"
)

func TestSupportTranscriptIsStaffOnly(t *testing.T) {
	p := testkit.Start(t)
	_, err := p.DB.Exec(`INSERT INTO clarifications(id,admin,created_at,updated_at) VALUES(90001,false,now(),now()); INSERT INTO discord_support_threads(clarification_id,thread_id,participant_ids,status,transcript) VALUES(90001,'thread','[]','closed','[{"content":"staff-only-secret"}]')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, staff := range []bool{false, true} {
		client := registrationClient(t, p, &session.Session{Staff: staff})
		for _, path := range []string{"/admin/support_threads", "/admin/support_threads/90001"} {
			res, err := client.Get(p.Server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			body := readAll(t, res)
			if staff && res.StatusCode != http.StatusOK {
				t.Fatalf("staff %s = %d", path, res.StatusCode)
			}
			if !staff && (res.StatusCode == http.StatusOK || strings.Contains(body, "staff-only-secret")) {
				t.Fatal("non-staff accessed transcript")
			}
			if staff && strings.HasSuffix(path, "/90001") && !strings.Contains(body, "staff-only-secret") {
				t.Fatal("staff transcript missing")
			}
		}
	}
}
