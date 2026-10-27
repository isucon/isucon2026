package loadfixture

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"time"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/model"
)

var tables = []string{"teams", "contestants", "contestant_instances", "benchmark_jobs", "benchmark_results", "notifications", "survey_response_history", "survey_responses"}

// WriteSQL emits a psql script for a migrated database with an unused ID range. Importing it
// neither runs workers nor contacts external services.
func (d *Dataset) WriteSQL(out io.Writer) error {
	w := bufio.NewWriter(out)
	id := func(n int) int { return d.Config.IDOffset + n }
	fmt.Fprintln(w, `\set ON_ERROR_STOP on
BEGIN;`)
	fmt.Fprintf(w, "LOCK TABLE %s IN ACCESS EXCLUSIVE MODE;\n", strings.Join(tables, ","))
	for _, table := range tables {
		fmt.Fprintf(w, "DO $$ BEGIN IF EXISTS (SELECT 1 FROM %s WHERE id > %d) THEN RAISE EXCEPTION 'loadfixture requires empty tables above id-offset: %s'; END IF; END $$;\n", table, d.Config.IDOffset, table)
	}
	at := d.Config.Start.Add(-time.Hour)
	best := make([]*Job, len(d.Teams))
	audience := make([]*Job, len(d.Teams))
	latest := make([]*Job, len(d.Teams))
	for i := range d.Jobs {
		j := &d.Jobs[i]
		n := j.TeamID - 1
		latest[n] = j
		if best[n] == nil || j.Score > best[n].Score {
			best[n] = j
		}
		if j.Finished.Before(d.freeze()) && (audience[n] == nil || j.Score > audience[n].Score) {
			audience[n] = j
		}
	}
	resultID := func(j *Job) any {
		if j == nil {
			return nil
		}
		return id(j.ID)
	}
	copyStart(w, "teams", "id,name,leader_id,email_address,invite_token,student,final_participation,created_at,updated_at,best_benchmark_result_id,best_benchmark_result_for_audience_id,best_benchmark_result_for_admin_id")
	for _, t := range d.Teams {
		name := fmt.Sprintf("load-team-%04d", id(t.ID))
		row(w, id(t.ID), name, id((t.ID-1)*3+1), name+"@example.invalid", name+"-fixture-only", t.ID%10 == 0, true, at, at, resultID(best[t.ID-1]), resultID(audience[t.ID-1]), resultID(best[t.ID-1]))
	}
	copyEnd(w)
	copyStart(w, "contestants", "id,team_id,name,github_login,github_id,discord_id,discord_tag,avatar_url,student,is_discord_guild_member,created_at,updated_at")
	for _, t := range d.Teams {
		for m := 1; m <= t.Members; m++ {
			memberID := id((t.ID-1)*3 + m)
			name := fmt.Sprintf("load-user-%04d", memberID)
			row(w, memberID, id(t.ID), name, name, name, name, name, "", t.ID%10 == 0, true, at, at)
		}
	}
	copyEnd(w)
	copyStart(w, "contestant_instances", "id,team_id,cloud_id,number,status,private_ipv4_address,created_at,updated_at")
	for _, t := range d.Teams {
		for n := 1; n <= 3; n++ {
			instanceID := id((t.ID-1)*3 + n)
			row(w, instanceID, id(t.ID), fmt.Sprintf("load-instance-%d", instanceID), n, 4, fmt.Sprintf("10.%d.%d.%d", ((t.ID-1)*3+n)/65536, (((t.ID-1)*3+n)/256)%256, ((t.ID-1)*3+n)%256), at, at)
		}
	}
	copyEnd(w)
	copyStart(w, "benchmark_jobs", "id,team_id,status,instance_name,handle,target_id,enqueued_by_id,created_at,started_at,finished_at,updated_at")
	for _, j := range d.Jobs {
		row(w, id(j.ID), id(j.TeamID), model.JobFinished, fmt.Sprintf("load-bench-%d", j.ID%100), fmt.Sprintf("load-handle-%d", id(j.ID)), id(j.TargetID), id(j.ContestantID), j.Created, j.Started, j.Finished, j.Finished)
	}
	copyEnd(w)
	copyStart(w, "benchmark_results", "id,team_id,benchmark_job_id,score,score_raw,score_deduction,finished,passed,marked_at,reason,stdout,stderr,exit_status,exit_signal,created_at,updated_at")
	for _, j := range d.Jobs {
		reason := ""
		if !j.Passed {
			reason = "synthetic validation failure"
		}
		row(w, id(j.ID), id(j.TeamID), id(j.ID), j.Score, j.Raw, j.Deduction, true, j.Passed, j.Finished, reason, logText(j, d.Config.LogBytes), reason, 0, nil, j.Started, j.Finished)
	}
	copyEnd(w)
	copyStart(w, "notifications", `id,contestant_id,"read",encoded_message,created_at,updated_at`)
	completed := append([]Job(nil), d.Jobs...)
	sort.SliceStable(completed, func(i, j int) bool { return completed[i].Finished.Before(completed[j].Finished) })
	notificationID := 0
	for _, j := range completed {
		encoded, err := model.EncodeNotificationMessage(&pb.Notification{Content: &pb.Notification_ContentBenchmarkJob{ContentBenchmarkJob: &pb.Notification_BenchmarkJobMessage{BenchmarkJobId: int64(id(j.ID))}}})
		if err != nil {
			return err
		}
		for m := 1; m <= d.Teams[j.TeamID-1].Members; m++ {
			notificationID++
			row(w, id(notificationID), id((j.TeamID-1)*3+m), j.Finished.Before(d.Config.Start.Add(d.Config.Duration-10*time.Minute)), encoded, j.Finished, j.Finished)
		}
	}
	copyEnd(w)
	copyStart(w, "survey_responses", "id,team_id,benchmark_job_id,language,created_at,updated_at")
	for _, t := range d.Teams {
		j := latest[t.ID-1]
		if j != nil {
			row(w, id(t.ID), id(t.ID), id(j.ID), "go", j.Finished, j.Finished)
		}
	}
	copyEnd(w)
	for _, table := range tables {
		fmt.Fprintf(w, "SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE(max(id),1), max(id) IS NOT NULL) FROM %s;\n", table, table)
	}
	fmt.Fprintln(w, "COMMIT;")
	for _, table := range tables {
		fmt.Fprintf(w, "ANALYZE %s;\n", table)
	}
	return w.Flush()
}

func copyStart(w io.Writer, table, columns string) {
	fmt.Fprintf(w, "COPY %s (%s) FROM stdin;\n", table, columns)
}
func copyEnd(w io.Writer) { fmt.Fprintln(w, `\.`) }

var copyEscape = strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\n", "\\n", "\r", "\\r")

func row(w io.Writer, values ...any) {
	for i, v := range values {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		if v == nil {
			fmt.Fprint(w, `\N`)
			continue
		}
		if t, ok := v.(time.Time); ok {
			v = t.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprint(w, copyEscape.Replace(fmt.Sprint(v)))
	}
	fmt.Fprintln(w)
}

// Vary the log content, including within each result, to avoid unrealistically
// tiny TOAST storage from repeating one constant padding string.
func logText(j Job, size int) string {
	r := rand.New(rand.NewSource(j.LogSeed))
	var b strings.Builder
	for b.Len() < size {
		fmt.Fprintf(&b, "request=%016x latency_us=%d status=200 team=%d job=%d\n", r.Uint64(), r.Intn(100000), j.TeamID, j.ID)
	}
	return b.String()[:size]
}
