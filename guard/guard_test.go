package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDetectorCountsFailuresWithinWindow(t *testing.T) {
	d := newDetector(5, 10*time.Second)
	base := time.Unix(1_800_000_000, 0)
	for i := range 4 {
		if d.observe("203.0.113.7", base.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("banned after %d failures", i+1)
		}
	}
	if !d.observe("203.0.113.7", base.Add(4*time.Second)) {
		t.Fatal("5 failures in 10s did not ban")
	}
	if d.observe("203.0.113.7", base.Add(5*time.Second)) {
		t.Fatal("counter was not reset after a ban")
	}

	// 3 秒おきの失敗は、どの 10 秒にも 4 回しか入らない。
	for i := range 10 {
		if d.observe("198.51.100.1", base.Add(time.Duration(3*i)*time.Second)) {
			t.Fatalf("failures every 3s banned at %d", i)
		}
	}

	// 失敗の止まった IP は、集計期間が過ぎたら捨てる。
	d.observe("192.0.2.1", base.Add(100*time.Second))
	if _, ok := d.failures["198.51.100.1"]; ok {
		t.Fatal("stale IP was not swept")
	}
}

func TestRunBansRepeatedLoginFailures(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	line := func(message string, offset time.Duration) string {
		entry := map[string]any{
			"MESSAGE":              message,
			"__REALTIME_TIMESTAMP": strconv.FormatInt(base.Add(offset).UnixMicro(), 10),
			"_SYSTEMD_UNIT":        "isureal-go.service",
			"PRIORITY":             "6",
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	failure := func(ip string) string {
		return `{"level":"info","action":"login","status":401,"ip":"` + ip + `","user_id":null,"username":"alice"}`
	}
	journal := strings.Join([]string{
		line(failure("203.0.113.7"), 0),
		line(failure("203.0.113.7"), time.Second),
		line(`{"level":"info","action":"login","status":200,"ip":"203.0.113.7","user_id":null,"username":"alice"}`, 2*time.Second),
		line(`{"level":"debug","action":"get_feed","status":200,"ip":"203.0.113.7","user_id":"u"}`, 2*time.Second),
		line(`{"time":"2026-10-17T00:00:00Z","level":"ERROR","msg":"request failed","error":"boom"}`, 2*time.Second),
		line("not json", 2*time.Second),
		`{"MESSAGE":[1,2,3],"__REALTIME_TIMESTAMP":"0"}`,
		"broken journal line",
		line(failure("203.0.113.7"), 3*time.Second),
		line(failure("198.51.100.1"), 3*time.Second),
		line(failure("203.0.113.7"), 4*time.Second),
		line(failure("203.0.113.7"), 5*time.Second),
	}, "\n")

	var banned []string
	ban := func(_ context.Context, ip string) error {
		banned = append(banned, ip)
		return nil
	}
	var output bytes.Buffer
	err := run(context.Background(), strings.NewReader(journal), newDetector(5, 10*time.Second), ban, log.New(&output, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(banned, []string{"203.0.113.7"}) {
		t.Fatalf("banned = %v", banned)
	}
	if !strings.Contains(output.String(), "banned: ip=203.0.113.7") {
		t.Fatalf("ban was not logged: %s", output.String())
	}
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.unit != "isureal-go.service" || cfg.threshold != 10 || cfg.window != 10*time.Second || cfg.banDuration != 2*time.Minute {
		t.Fatalf("defaults = %+v", cfg)
	}
	for key, value := range map[string]string{
		"GUARD_FAIL_THRESHOLD": "0", "GUARD_FAIL_WINDOW": "ten", "GUARD_BAN_DURATION": "-1m",
	} {
		if _, err := loadConfig(func(k string) string {
			if k == key {
				return value
			}
			return ""
		}); err == nil {
			t.Fatalf("%s=%s was accepted", key, value)
		}
	}
}

func TestDBBannerIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schemaFile, err := os.Open("../webapp/sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := io.ReadAll(schemaFile)
	schemaFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}

	banner := &dbBanner{db: db, duration: 2 * time.Minute}
	if _, err := db.Exec(`INSERT INTO ip_bans (ip, banned_until) VALUES ('198.51.100.1', clock_timestamp() + interval '10 minutes')`); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"203.0.113.7", "203.0.113.7", "198.51.100.1"} {
		if err := banner.ban(context.Background(), ip); err != nil {
			t.Fatal(err)
		}
	}
	remaining := func(ip string) time.Duration {
		t.Helper()
		var seconds float64
		if err := db.QueryRow(`SELECT extract(epoch FROM banned_until - clock_timestamp()) FROM ip_bans WHERE ip = $1`, ip).Scan(&seconds); err != nil {
			t.Fatal(err)
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if got := remaining("203.0.113.7"); got < 110*time.Second || got > 2*time.Minute {
		t.Fatalf("ban lasts %s, want about 2m", got)
	}
	// 既存の長い BAN は短くしない。
	if got := remaining("198.51.100.1"); got < 9*time.Minute {
		t.Fatalf("longer ban was shortened to %s", got)
	}
}
