package loadfixture

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// A private schema keeps this test independent of existing portal data.
func TestSQLImport(t *testing.T) {
	databaseURL := os.Getenv("ISUXPORTAL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ISUXPORTAL_TEST_DATABASE_URL is not set")
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Fatal("psql is required for fixture integration tests")
	}
	ctx := context.Background()
	admin, err := db.Open(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("loadfixture_test_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	u, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema)
	u.RawQuery = q.Encode()
	conn, err := db.Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Teams = 3
	d, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	var script bytes.Buffer
	if err = d.WriteSQL(&script); err != nil {
		t.Fatal(err)
	}
	importSQL := func() ([]byte, error) {
		cmd := exec.Command("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", u.String())
		cmd.Stdin = bytes.NewReader(script.Bytes())
		return cmd.CombinedOutput()
	}
	if out, err := importSQL(); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	for table, want := range map[string]int{"teams": 3, "benchmark_jobs": len(d.Jobs), "benchmark_results": len(d.Jobs), "notifications": d.Summary().Notifications, "contestants": d.Summary().Contestants, "contestant_instances": 9} {
		var got int
		if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	var invalid int
	if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM benchmark_jobs j
 LEFT JOIN contestants c ON c.id=j.enqueued_by_id AND c.team_id=j.team_id
 LEFT JOIN contestant_instances i ON i.id=j.target_id AND i.team_id=j.team_id
 LEFT JOIN benchmark_results r ON r.benchmark_job_id=j.id AND r.team_id=j.team_id
 WHERE c.id IS NULL OR i.id IS NULL OR r.id IS NULL OR j.status<>4 OR NOT r.finished
 OR r.marked_at<>j.finished_at OR octet_length(r.stdout)<>$1`, c.LogBytes).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatalf("invalid references/payloads=%d err=%v", invalid, err)
	}
	// The real leaderboard maintenance must produce equivalent scores and visibility.
	freeze, end := d.freeze(), c.Start.Add(c.Duration)
	bestIDs := func(teamID int) [3]int64 {
		t.Helper()
		var ids [3]int64
		if err := conn.QueryRowContext(ctx, "SELECT best_benchmark_result_id,best_benchmark_result_for_audience_id,best_benchmark_result_for_admin_id FROM teams WHERE id=$1", teamID).Scan(&ids[0], &ids[1], &ids[2]); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	for _, team := range d.Teams {
		before := bestIDs(team.ID)
		if err = contest.MaintainBestForTeam(ctx, conn, config.Timing{ContestStart: &c.Start, ContestFreeze: &freeze, ContestEnd: &end}, int64(team.ID), nil); err != nil {
			t.Fatal(err)
		}
		if after := bestIDs(team.ID); before != after {
			t.Fatalf("best results: before=%v after=%v", before, after)
		}
	}

	ns, err := model.QueryNotifications(ctx, conn, `SELECT id,contestant_id,"read",encoded_message,created_at FROM notifications ORDER BY id LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range ns {
		p, err := n.ToPB()
		if err != nil || p.GetContentBenchmarkJob().GetBenchmarkJobId() == 0 {
			t.Fatalf("invalid notification: %v", err)
		}
	}
	var next int
	if err = conn.QueryRowContext(ctx, "SELECT nextval(pg_get_serial_sequence('benchmark_jobs','id'))").Scan(&next); err != nil || next != len(d.Jobs)+1 {
		t.Fatalf("sequence=%d err=%v", next, err)
	}
	if out, err := importSQL(); err == nil || !strings.Contains(string(out), "requires empty tables") {
		t.Fatalf("second import was not rejected: %v %s", err, out)
	}
	// Append a second fixture without changing existing IDs or rows.
	c.IDOffset = 1000000
	added, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	script.Reset()
	if err = added.WriteSQL(&script); err != nil {
		t.Fatal(err)
	}
	if out, err := importSQL(); err != nil {
		t.Fatalf("append: %v %s", err, out)
	}
	var linked int
	if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM benchmark_jobs j
 JOIN contestants c ON c.id=j.enqueued_by_id AND c.team_id=j.team_id
 JOIN contestant_instances i ON i.id=j.target_id AND i.team_id=j.team_id
 JOIN benchmark_results r ON r.benchmark_job_id=j.id AND r.team_id=j.team_id
 WHERE j.id>1000000`).Scan(&linked); err != nil || linked != len(d.Jobs) {
		t.Fatalf("appended links=%d err=%v", linked, err)
	}
	ns, err = model.QueryNotifications(ctx, conn, `SELECT id,contestant_id,"read",encoded_message,created_at FROM notifications WHERE id>1000000 LIMIT 1`)
	if err != nil || len(ns) != 1 {
		t.Fatalf("appended notification: %v", err)
	}
	n, err := ns[0].ToPB()
	if err != nil || n.GetContentBenchmarkJob().GetBenchmarkJobId() <= 1000000 || ns[0].ContestantID <= 1000000 {
		t.Fatal("notification references not shifted")
	}
	if out, err := importSQL(); err == nil {
		t.Fatalf("duplicate append accepted: %s", out)
	}
	// Refresh scores without removing jobs, notifications, or unrelated rows.
	added.Jobs[0].Passed = true
	added.Jobs[0].Raw = 999999
	added.Jobs[0].Deduction = 0
	added.Jobs[0].Score = 999999
	script.Reset()
	if err = added.WriteScoreUpdateSQL(&script); err != nil {
		t.Fatal(err)
	}
	if out, err := importSQL(); err != nil {
		t.Fatalf("score update: %v %s", err, out)
	}
	var score int
	if err = conn.QueryRowContext(ctx, "SELECT score FROM benchmark_results WHERE benchmark_job_id=1000001").Scan(&score); err != nil || score != 999999 {
		t.Fatalf("updated score=%d err=%v", score, err)
	}
	original, err := model.FindResultByJob(ctx, conn, 1)
	if err != nil || original.Score != int64(d.Jobs[0].Score) {
		t.Fatal("unrelated result changed")
	}
	// A wrong schedule must fail before modifying any existing score.
	added.Jobs[0].Created = added.Jobs[0].Created.Add(time.Second)
	script.Reset()
	if err = added.WriteScoreUpdateSQL(&script); err != nil {
		t.Fatal(err)
	}
	if out, err := importSQL(); err == nil {
		t.Fatalf("mismatched schedule accepted: %s", out)
	}
	var count int
	if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM benchmark_results").Scan(&count); err != nil || count != 2*len(d.Jobs) {
		t.Fatal("second import modified data")
	}
}
