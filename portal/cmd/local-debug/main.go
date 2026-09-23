// local-debug manages the isolated Compose fixture. It is not a public API.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/session"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const fixtureIdentity = "local-debug-fixture"

type fixture struct {
	TeamID           int64  `json:"team_id"`
	ContestantID     int64  `json:"contestant_id"`
	AvailabilityZone string `json:"availability_zone"`
	TargetIPv4       string `json:"target_ipv4"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if os.Getenv("PORTAL_LOCAL_DEBUG") != "1" {
		return fmt.Errorf("PORTAL_LOCAL_DEBUG=1 is required; use the isolated local-debug Compose environment")
	}
	if len(args) != 1 || (args[0] != "register" && args[0] != "enqueue" && args[0] != "status") {
		return fmt.Errorf("usage: local-debug register|enqueue|status")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	conn, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if args[0] == "register" {
		host := os.Getenv("PORTAL_LOCAL_TARGET")
		if host == "" {
			return fmt.Errorf("PORTAL_LOCAL_TARGET is required")
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil {
			return err
		}
		if len(ips) == 0 {
			return fmt.Errorf("no IPv4 for %s", host)
		}
		f, err := register(ctx, conn, ips[0].String())
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(f)
	}
	var teamID int64
	if err := conn.QueryRowContext(ctx, `SELECT team_id FROM contestants WHERE github_id=$1`, fixtureIdentity).Scan(&teamID); err != nil {
		return fmt.Errorf("run debug-register first: %w", err)
	}
	endpoint := os.Getenv("PORTAL_LOCAL_ENDPOINT")
	if endpoint == "" {
		return fmt.Errorf("PORTAL_LOCAL_ENDPOINT is required")
	}
	var response proto.Message
	path := "/api/admin/benchmark_jobs"
	method := http.MethodPost
	var body proto.Message = &pbadmin.EnqueueBenchmarkJobRequest{TeamId: teamID, TargetNumber: 1}
	if args[0] == "status" {
		var jobID int64
		if err := conn.QueryRowContext(ctx, `SELECT id FROM benchmark_jobs WHERE team_id=$1 ORDER BY id DESC LIMIT 1`, teamID).Scan(&jobID); err != nil {
			return fmt.Errorf("no benchmark job; run debug-bench first: %w", err)
		}
		path = fmt.Sprintf("%s/%d", path, jobID)
		method = http.MethodGet
		body = nil
		response = &pbadmin.GetBenchmarkJobResponse{}
	} else {
		response = &pbadmin.EnqueueBenchmarkJobResponse{}
	}
	if err := callAPI(ctx, cfg, endpoint, method, path, body, response); err != nil {
		return err
	}
	data, err := protojson.MarshalOptions{Multiline: true, EmitUnpopulated: true}.Marshal(response)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

func register(ctx context.Context, conn *sql.DB, address string) (*fixture, error) {
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("target must be IPv4")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize repeated/concurrent fixture registration without changing the production schema.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(2026092401)`); err != nil {
		return nil, err
	}
	f := &fixture{AvailabilityZone: "apne1-az1", TargetIPv4: address}
	err = tx.QueryRowContext(ctx, `SELECT id,team_id FROM contestants WHERE github_id=$1`, fixtureIdentity).Scan(&f.ContestantID, &f.TeamID)
	if err == sql.ErrNoRows {
		err = tx.QueryRowContext(ctx, `INSERT INTO teams(name,email_address,invite_token,student,final_participation,availability_zone,created_at,updated_at) VALUES('Local debug team','local-debug@example.invalid',$1,false,true,$2,now(),now()) RETURNING id`, model.NewInviteToken(), f.AvailabilityZone).Scan(&f.TeamID)
		if err != nil {
			return nil, err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO contestants(team_id,name,github_login,github_id,discord_id,discord_tag,avatar_url,student,created_at,updated_at) VALUES($1,'Local debug contestant','local-debug',$2,$2,'local-debug','',false,now(),now()) RETURNING id`, f.TeamID, fixtureIdentity).Scan(&f.ContestantID)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE teams SET leader_id=$1,availability_zone=$2,updated_at=now() WHERE id=$3`, f.ContestantID, f.AvailabilityZone, f.TeamID); err != nil {
		return nil, err
	}
	for n := 1; n <= 3; n++ {
		_, err = tx.ExecContext(ctx, `INSERT INTO contestant_instances(team_id,cloud_id,number,status,private_ipv4_address,public_ipv4_address,created_at,updated_at) VALUES($1,$2,$3,4,$4,$4,now(),now()) ON CONFLICT(team_id,number) DO UPDATE SET private_ipv4_address=EXCLUDED.private_ipv4_address,public_ipv4_address=EXCLUDED.public_ipv4_address,updated_at=now()`, f.TeamID, fmt.Sprintf("local-debug-%d", n), n, address)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

// Issue a normal staff session locally, then exercise the real API and CSRF guard.
func callAPI(ctx context.Context, cfg *config.Config, endpoint, method, path string, body, response proto.Message) error {
	store, err := session.NewStore(cfg.RedisURL, cfg.SessionSecret)
	if err != nil {
		return err
	}
	defer store.Redis().Close()
	sess := &session.Session{Staff: true}
	csrf := sess.EnsureCSRFToken()
	rec := httptest.NewRecorder()
	if err = store.Save(ctx, rec, sess); err != nil {
		return err
	}
	var data []byte
	if body != nil {
		data, err = proto.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	req.Header.Set("Content-Type", "application/vnd.google.protobuf")
	req.Header.Set("X-CSRF-Token", csrf)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: HTTP %d (duplicate enqueue returns 422)", method, path, res.StatusCode)
	}
	return proto.Unmarshal(data, response)
}
