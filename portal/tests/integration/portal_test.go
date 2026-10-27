// Package integration_test は MySQL + Redis を使った HTTP 越しの結合テスト。
// backend/tests/integration と同じく portal-integration-migration ジョブで動く。
// 受け入れの主戦力は rspec/（HTTP 化した contest_spec ほか）で、
// ここは Ruby 無しの CI でも守れる中核経路を検証する。
package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbaudience "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/audience"
	pbbench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	pbcontestant "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/contestant"
	pbregistration "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestAudienceDashboardContentType(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()

	res := p.DoPB(t, http.MethodGet, "/api/audience/dashboard", nil, testkit.TimingHeaders(now))
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	// CT-1: Content-Type は application/protobuf; proto=<full message name>
	want := "application/protobuf; proto=isuxportal.proto.services.audience.DashboardResponse"
	if got := res.Header.Get("Content-Type"); got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
}

func TestRegistrationSessionNotLoggedIn(t *testing.T) {
	p := testkit.Start(t)
	res := p.DoPB(t, http.MethodGet, "/api/registration/session", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	body := &pbregistration.GetRegistrationSessionResponse{}
	testkit.DecodePB(t, res, body)
	if body.Status != pbregistration.GetRegistrationSessionResponse_NOT_LOGGED_IN {
		t.Errorf("status = %v, want NOT_LOGGED_IN", body.Status)
	}
}

// enqueue → 払い出し → 途中報告（センチネル CT-7）→ 確定報告 → best 更新・通知・キャッシュ 304。
func TestBenchCycle(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()
	timing := testkit.TimingHeaders(now)
	teamID, leaderID := p.CreateTeam(t, "cycle")
	var instanceID int64
	var addresses []string
	for n := int64(1); n <= 4; n++ {
		instanceID = p.CreateInstance(t, teamID, n)
		address := fmt.Sprintf("203.0.113.%d", n)
		if _, err := p.DB.Exec(`UPDATE contestant_instances SET public_ipv4_address = $1 WHERE id = $2`, address, instanceID); err != nil {
			t.Fatal(err)
		}
		addresses = append(addresses, address)
	}

	session := map[string]string{"X-Isux-Session-Contestant-Id": fmt.Sprint(leaderID)}
	for k, v := range timing {
		session[k] = v
	}

	// enqueue（FR-C-2）
	res := p.DoPB(t, http.MethodPost, "/api/contestant/benchmark_jobs",
		&pbcontestant.EnqueueBenchmarkJobRequest{TargetId: instanceID}, session)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enqueue status = %d", res.StatusCode)
	}
	enqueued := &pbcontestant.EnqueueBenchmarkJobResponse{}
	testkit.DecodePB(t, res, enqueued)

	// 払い出し（FR-B-1）
	res = p.DoPB(t, http.MethodPost, "/api/bench/receive",
		&pbbench.ReceiveBenchmarkJobRequest{Token: "token", InstanceName: "apne1-az1"}, timing)
	received := &pbbench.ReceiveBenchmarkJobResponse{}
	testkit.DecodePB(t, res, received)
	if received.JobHandle == nil || received.JobHandle.JobId != enqueued.Job.Id {
		t.Fatalf("job handle = %v", received.JobHandle)
	}

	if !slices.Equal(received.JobHandle.AllIpv4Addresses, addresses) {
		t.Fatalf("addresses = %v, want %v", received.JobHandle.AllIpv4Addresses, addresses)
	}
	if received.JobHandle.TargetIpv4Address != addresses[3] {
		t.Fatalf("target = %q, want fourth server %q", received.JobHandle.TargetIpv4Address, addresses[3])
	}

	// 途中報告: score == -1 → raw - deduction（CT-7）
	res = p.DoPB(t, http.MethodPost, "/api/bench/benchmark_results",
		&pbbench.ReportBenchmarkResultRequest{
			JobId:  received.JobHandle.JobId,
			Handle: received.JobHandle.Handle,
			Result: &pbresources.BenchmarkResult{
				Score:          -1,
				ScoreBreakdown: &pbresources.BenchmarkResult_ScoreBreakdown{Raw: 100, Deduction: 10},
				SurveyResponse: &pbresources.SurveyResponse{Language: "go", Agent: "human", Harness: "manual", Model: "model-a,model-b"},
				MarkedAt:       timestamppb.New(now),
			},
		}, timing)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("progress report status = %d", res.StatusCode)
	}
	var score int64
	if err := p.DB.QueryRow(`SELECT score FROM benchmark_results WHERE benchmark_job_id = $1`, enqueued.Job.Id).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score != 90 {
		t.Errorf("sentinel score = %d, want 90", score)
	}

	// 確定報告（FR-B-4）
	res = p.DoPB(t, http.MethodPost, "/api/bench/benchmark_results/completion",
		&pbbench.CompleteBenchmarkJobRequest{
			JobId:  received.JobHandle.JobId,
			Handle: received.JobHandle.Handle,
			Result: &pbresources.BenchmarkResult{
				Finished:       true,
				Passed:         true,
				Score:          -1,
				ScoreBreakdown: &pbresources.BenchmarkResult_ScoreBreakdown{Raw: 500, Deduction: 50},
				SurveyResponse: &pbresources.SurveyResponse{Language: "go", Agent: "human", Harness: "manual", Model: "model-a,model-b"},
				MarkedAt:       timestamppb.New(now.Add(time.Minute)),
				Execution:      &pbresources.BenchmarkResult_Execution{ExitStatus: 0, Stdout: "ok"},
			},
		}, timing)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("completion status = %d", res.StatusCode)
	}

	for _, table := range []string{"survey_responses", "survey_response_history"} {
		var count int
		if err := p.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE team_id=$1 AND benchmark_job_id=$2 AND language='go' AND agent='human' AND harness='manual' AND model='model-a,model-b'`, teamID, enqueued.Job.Id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s survey count=%d", table, count)
		}
	}
	var status int32
	var bestID *int64
	if err := p.DB.QueryRow(`SELECT status FROM benchmark_jobs WHERE id = $1`, enqueued.Job.Id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != int32(pbresources.BenchmarkJob_FINISHED) {
		t.Errorf("job status = %d, want FINISHED", status)
	}
	// BenchmarkCompletionJob（同期実行）が best カラムを更新している（DM-8）
	if err := p.DB.QueryRow(`SELECT best_benchmark_result_id FROM teams WHERE id = $1`, teamID).Scan(&bestID); err != nil {
		t.Fatal(err)
	}
	if bestID == nil {
		t.Error("best_benchmark_result_id must be maintained after completion")
	}
	// メンバーに通知が作られている
	var notifications int
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE contestant_id = $1`, leaderID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Errorf("notifications = %d, want 1", notifications)
	}

	// ダッシュボードは事前生成キャッシュから返り、ETag 一致で 304（CT-5 / NF-1）
	res = p.DoPB(t, http.MethodGet, "/api/audience/dashboard", nil, timing)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", res.StatusCode)
	}
	dashboard := &pbaudience.DashboardResponse{}
	etag := res.Header.Get("ETag")
	testkit.DecodePB(t, res, dashboard)
	if len(dashboard.Leaderboard.Teams) != 1 || dashboard.Leaderboard.Teams[0].BestScore.Score != 450 {
		t.Fatalf("leaderboard = %v", dashboard.Leaderboard.Teams)
	}
	headers := map[string]string{"If-None-Match": etag}
	for k, v := range timing {
		headers[k] = v
	}
	res = p.DoPB(t, http.MethodGet, "/api/audience/dashboard", nil, headers)
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("dashboard with If-None-Match = %d, want 304", res.StatusCode)
	}
}

// FR-E: 非 protobuf 3 本の形式（JSON / text/plain / 空ボディ）。
func TestEnvCheckEndpoints(t *testing.T) {
	p := testkit.Start(t)
	now := time.Now()
	timing := testkit.TimingHeaders(now)
	teamID, leaderID := p.CreateTeam(t, "envcheck")

	tokenParam := checkerTokenParam(t, p, teamID)

	res := p.DoPB(t, http.MethodGet, "/api/env_check_info?name=test-boot&token="+tokenParam, nil, timing)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("env_check_info status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("env_check_info Content-Type = %q, want JSON", ct)
	}

	if _, err := p.DB.Exec(`INSERT INTO ssh_public_keys (contestant_id, public_key, created_at, updated_at) VALUES ($1, 'ssh-ed25519 KEY', now(), now())`, leaderID); err != nil {
		t.Fatal(err)
	}
	res2, err := http.Get(p.Server.URL + "/api/ssh_public_keys?token=" + tokenParam)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, res2)
	if ct := res2.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("ssh_public_keys Content-Type = %q, want text/plain", ct)
	}
	if !strings.HasSuffix(body, "\n") || !strings.Contains(body, fmt.Sprintf("ssh-ed25519 KEY %d@", leaderID)) {
		t.Errorf("ssh_public_keys body = %q", body)
	}

	bodyJSON, err := json.Marshal(map[string]any{
		"token": tokenParam, "name": "contest1", "ip_address": "203.0.113.9",
		"passed": true, "message": "m", "admin_message": "am", "raw_data": "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, p.Server.URL+"/api/env_checks", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range timing {
		req.Header.Set(k, v)
	}
	res3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAll(t, res3); res3.StatusCode != http.StatusNoContent || body != "" {
		t.Errorf("env_checks = %d %q, want 204 empty", res3.StatusCode, body)
	}
	// 副作用: ContestantInstance がダミー値で登録される（FR-E-5）
	var cloudID, privateIP string
	if err := p.DB.QueryRow(`SELECT cloud_id, private_ipv4_address FROM contestant_instances WHERE team_id = $1 AND number = 1`, teamID).Scan(&cloudID, &privateIP); err != nil {
		t.Fatal(err)
	}
	if cloudID != fmt.Sprintf("contest-%d-1", teamID) || privateIP != "192.168.0.11" {
		t.Errorf("instance = %s / %s", cloudID, privateIP)
	}
}
