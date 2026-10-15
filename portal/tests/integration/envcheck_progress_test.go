package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	registration "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestEnvCheckProgressHTTP(t *testing.T) {
	p := testkit.Start(t)
	team, leader := p.CreateTeam(t, "progress")
	_, otherLeader := p.CreateTeam(t, "other-progress")
	headers := map[string]string{"X-Isux-Session-Contestant-Id": fmt.Sprint(leader)}
	assertStatus := func(want pb.EnvCheckStatus, ips int) {
		t.Helper()
		response := p.DoPB(t, http.MethodGet, "/api/registration/env_check", nil, headers)
		body := &registration.GetEnvCheckInformationResponse{}
		testkit.DecodePB(t, response, body)
		if body.Status != want || len(body.InstanceIps) != ips {
			t.Fatalf("envcheck: %v, IPs %v; want %v/%d", body.Status, body.InstanceIps, want, ips)
		}
		session := &registration.GetRegistrationSessionResponse{}
		testkit.DecodePB(t, p.DoPB(t, http.MethodGet, "/api/registration/session", nil, headers), session)
		if session.EnvCheckStatus != want {
			t.Fatalf("session status=%v, want %v", session.EnvCheckStatus, want)
		}
	}
	post := func(n int, name string, passed bool) {
		t.Helper()
		instances := []map[string]string{}
		for j := 1; j <= 5; j++ {
			instances = append(instances, map[string]string{"instance_id": fmt.Sprintf("i-%d", j)})
		}
		raw, _ := json.Marshal(map[string]any{"instance_id": fmt.Sprintf("i-%d", n), "instances": instances})
		payload, _ := json.Marshal(map[string]any{"token": checkerTokenParam(t, p, team), "name": name, "passed": passed, "ip_address": fmt.Sprintf("203.0.113.%d", n), "raw_data": string(raw)})
		response, err := p.Server.Client().Post(p.Server.URL+"/api/env_checks", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		if body := readAll(t, response); response.StatusCode != 204 {
			t.Fatalf("POST: %d %s", response.StatusCode, body)
		}
	}
	assertStatus(pb.EnvCheckStatus_NOT_STARTED, 0)
	for n := 1; n <= 5; n++ {
		post(n, "test-boot", true)
	}
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE, 5)
	post(1, "test-ssh", true)
	assertStatus(pb.EnvCheckStatus_DONE, 5)
	other := &registration.GetRegistrationSessionResponse{}
	testkit.DecodePB(t, p.DoPB(t, http.MethodGet, "/api/registration/session", nil, map[string]string{"X-Isux-Session-Contestant-Id": fmt.Sprint(otherLeader)}), other)
	if other.EnvCheckStatus != pb.EnvCheckStatus_NOT_STARTED {
		t.Fatalf("other team inherited progress: %v", other.EnvCheckStatus)
	}
	post(1, "test-ssh", false)
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE, 5)
	p.Cfg.TestAMIID = ""
	assertStatus(pb.EnvCheckStatus_PREPARING, 0)
}
