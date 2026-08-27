// Package testkit は portal のテスト（integration / concurrency）共通の土台。
// backend/tests/testkit と同じ流儀:
//   - 接続先の環境変数が未設定ならテストを skip する（ローカルの go test ./... を守る）
//   - MySQL のロックでテストパッケージ間を直列化する
package testkit

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/isucon/isucon2026/portal/internal/cache"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/handler"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/session"
)

const lockName = "isuxportal_test"

var tablesToClean = []string{
	"benchmark_jobs", "benchmark_results", "clarifications", "contestant_instances", "contestants",
	"coupons", "env_checks", "extra_time_assignments", "notifications", "push_subscriptions",
	"ssh_public_keys", "survey_responses", "teams",
	"instance_command_execute_requests", "instance_command_execute_request_results",
}

// Database は MySQL に接続し、スキーマ適用と全テーブルの初期化を行う。
// ISUXPORTAL_TEST_DB_DSN が未設定なら skip（backend と同じ流儀）。
func Database(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ISUXPORTAL_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("ISUXPORTAL_TEST_DB_DSN is not set")
	}
	if !strings.Contains(dsn, "?") {
		dsn += "?parseTime=true&loc=UTC&multiStatements=true&charset=utf8mb4"
	}
	conn, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// テストパッケージ間の直列化（backend の advisory lock 相当）。
	// GET_LOCK はコネクション単位なので専用コネクションを test 終了まで握る。
	lockConn, err := conn.Conn(ctx)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	var got int
	if err := lockConn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 120)`, lockName).Scan(&got); err != nil || got != 1 {
		lockConn.Close()
		conn.Close()
		t.Fatalf("failed to acquire test lock: got=%d err=%v", got, err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(context.Background(), `SELECT RELEASE_LOCK(?)`, lockName)
		lockConn.Close()
		conn.Close()
	})

	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	Reset(t, conn)
	return conn
}

// Reset は全テーブルを空にする。
func Reset(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		t.Fatal(err)
	}
	for _, table := range tablesToClean {
		if _, err := conn.ExecContext(ctx, `TRUNCATE TABLE `+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 1`); err != nil {
		t.Fatal(err)
	}
}

// Config はテスト用の設定（テストモード有効・isucon14 由来のダミー secret）。
func Config(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		PublicURLHost:     "portal.test.invalid",
		Revision:          "test",
		MaxTeams:          600,
		BenchToken:        "token",
		BypassTokenSecret: mustBase64(t, "5AQQyz/7oFnJYrxnq/HiLKYmh7QrdIrpCDpKlfvkYAg="), //gitleaks:allow isucon14 由来のダミー値
		CheckerSecret:     mustBase64(t, "2RHAlLoifERG1EEc/5dwyzcZGitfUn1k9YkHTwk7HiRO"), //gitleaks:allow isucon14 由来のダミー値
		SSHKeyAPISecret:   "himitsudayo",
		SessionSecret:     "testkit-session-secret",
		AvailabilityZones: []string{"apne1-az1", "apne1-az2", "apne1-az4"},
		TestAMIID:         "ami-test",
		ContestAMIID:      "ami-y",
		AWSRegion:         "ap-northeast-1",
		VAPIDSubject:      "isucon10@googlegroups.com",
		TestMode:          true,
	}
}

// Portal は httptest サーバとして起動した portal 一式。
type Portal struct {
	DB     *sql.DB
	Cfg    *config.Config
	Server *httptest.Server
	Jobs   *job.Env
}

// Start は MySQL + Redis に接続してハンドラ一式を httptest で起動する。
// ISUXPORTAL_TEST_REDIS_URL が未設定なら skip。
func Start(t *testing.T) *Portal {
	t.Helper()
	redisURL := os.Getenv("ISUXPORTAL_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("ISUXPORTAL_TEST_REDIS_URL is not set")
	}
	conn := Database(t)
	cfg := Config(t)
	cfg.RedisURL = redisURL

	store, err := session.NewStore(cfg.RedisURL, cfg.SessionSecret)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("redis unavailable: %v", err)
	}
	if err := store.Redis().FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	push, err := external.NewWebPush("", cfg.VAPIDSubject)
	if err != nil {
		t.Fatal(err)
	}
	jobs := &job.Env{
		DB:      conn,
		Cfg:     cfg,
		Cache:   cache.New(store.Redis()),
		Slack:   &external.Slack{},
		Discord: &external.Discord{},
		GitHub:  &external.GitHub{},
		Push:    push,
		SSH:     &external.SSHExecutor{},
	}
	// テストではジョブを同期実行する（副作用を決定的に検証するため）
	jobs.SetQueue(&job.InProcessQueue{Env: jobs, Sync: true})

	server := httptest.NewServer(handler.New(cfg, conn, store, jobs.Cache, jobs, push, nil).Router())
	t.Cleanup(server.Close)

	return &Portal{DB: conn, Cfg: cfg, Server: server, Jobs: jobs}
}

// --- fixtures（RSpec の factory 相当。DB へ直接書く） ---

// CreateTeam はチームと代表者を作って (teamID, leaderContestantID) を返す。
func (p *Portal) CreateTeam(t *testing.T, name string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	res, err := p.DB.ExecContext(ctx, `
		INSERT INTO teams (name, email_address, invite_token, is_hidden, final_participation, student, availability_zone, created_at, updated_at)
		VALUES (?, ?, ?, 0, 1, 0, 'apne1-az1', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		name, name+"@test.invalid", model.NewInviteToken())
	if err != nil {
		t.Fatal(err)
	}
	teamID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	res, err = p.DB.ExecContext(ctx, `
		INSERT INTO contestants (team_id, name, github_login, github_id, discord_id, discord_tag, avatar_url, student, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'https://test.invalid/avatar', 0, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		teamID, name+"-leader", name+"-gh", "gh-"+name, "dc-"+name, name+"#0001")
	if err != nil {
		t.Fatal(err)
	}
	contestantID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.ExecContext(ctx, `UPDATE teams SET leader_id = ? WHERE id = ?`, contestantID, teamID); err != nil {
		t.Fatal(err)
	}
	return teamID, contestantID
}

// CreateInstance は ContestantInstance を作って id を返す。
func (p *Portal) CreateInstance(t *testing.T, teamID, number int64) int64 {
	t.Helper()
	res, err := p.DB.ExecContext(context.Background(), `
		INSERT INTO contestant_instances (team_id, cloud_id, number, status, private_ipv4_address, public_ipv4_address, created_at, updated_at)
		VALUES (?, ?, ?, 4, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		teamID, "cloud-test", number, "10.1.0.1", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TimingHeaders は競技中（開始 1h 前・凍結 1h 後・終了 2h 後）のテストモードヘッダ。
func TimingHeaders(now time.Time) map[string]string {
	format := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	return map[string]string{
		"X-Isux-Timing-Contest-Start":  format(now.Add(-time.Hour)),
		"X-Isux-Timing-Contest-Freeze": format(now.Add(time.Hour)),
		"X-Isux-Timing-Contest-End":    format(now.Add(2 * time.Hour)),
	}
}

// TryPB は protobuf ボディで HTTP リクエストを送る（goroutine から使える err 返却版）。
func (p *Portal) TryPB(method, path string, msg proto.Message, headers map[string]string) (*http.Response, error) {
	var body io.Reader
	if msg != nil {
		raw, err := proto.Marshal(msg)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, p.Server.URL+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return p.Server.Client().Do(req)
}

// DoPB は TryPB の test 用ラッパ。テスト goroutine 以外から呼んではならない。
func (p *Portal) DoPB(t *testing.T, method, path string, msg proto.Message, headers map[string]string) *http.Response {
	t.Helper()
	res, err := p.TryPB(method, path, msg, headers)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TryDecodePB はレスポンスボディを protobuf としてデコードする（err 返却版）。
func TryDecodePB(res *http.Response, msg proto.Message) error {
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	return proto.Unmarshal(raw, msg)
}

// DecodePB は TryDecodePB の test 用ラッパ。テスト goroutine 以外から呼んではならない。
func DecodePB(t *testing.T, res *http.Response, msg proto.Message) {
	t.Helper()
	if err := TryDecodePB(res, msg); err != nil {
		t.Fatalf("failed to decode %T: %v", msg, err)
	}
}

func mustBase64(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
