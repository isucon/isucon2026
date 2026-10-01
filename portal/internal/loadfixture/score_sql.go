package loadfixture

import (
	"bufio"
	"fmt"
	"io"
)

// WriteScoreUpdateSQL refreshes a previously generated fixture in place. Exact
// job/team/time matching prevents applying a different seed's results to it.
func (d *Dataset) WriteScoreUpdateSQL(out io.Writer) error {
	w := bufio.NewWriter(out)
	fmt.Fprintln(w, `\set ON_ERROR_STOP on
BEGIN;
LOCK TABLE teams, benchmark_jobs, benchmark_results IN SHARE ROW EXCLUSIVE MODE;
CREATE TEMP TABLE fixture_scores (id bigint PRIMARY KEY, team_id bigint, created_at timestamptz, score integer, raw integer, deduction integer, passed boolean, reason text) ON COMMIT DROP;`)
	copyStart(w, "fixture_scores", "id,team_id,created_at,score,raw,deduction,passed,reason")
	for _, j := range d.Jobs {
		reason := ""
		if !j.Passed {
			reason = "synthetic validation failure"
		}
		row(w, j.ID+d.Config.IDOffset, j.TeamID+d.Config.IDOffset, j.Created, j.Score, j.Raw, j.Deduction, j.Passed, reason)
	}
	copyEnd(w)
	fmt.Fprintln(w, `DO $$ BEGIN
IF EXISTS (
 SELECT 1 FROM fixture_scores s
 LEFT JOIN benchmark_jobs j ON j.id=s.id AND j.team_id=s.team_id AND j.created_at=s.created_at
 LEFT JOIN benchmark_results r ON r.benchmark_job_id=s.id AND r.team_id=s.team_id
 LEFT JOIN teams t ON t.id=s.team_id AND t.name='load-team-' || lpad(t.id::text, greatest(4,length(t.id::text)), '0')
 WHERE j.id IS NULL OR r.id IS NULL OR t.id IS NULL
) THEN RAISE EXCEPTION 'fixture job schedule does not match; use the original start, seed and ID offset'; END IF;
END $$;
UPDATE benchmark_results r SET score=s.score, score_raw=s.raw, score_deduction=s.deduction,
 passed=s.passed, reason=s.reason, stderr=s.reason
FROM fixture_scores s WHERE r.benchmark_job_id=s.id;
UPDATE teams t SET
 best_benchmark_result_for_admin_id=(SELECT id FROM benchmark_results r WHERE r.team_id=t.id AND r.finished AND r.exit_status=0 AND r.exit_signal IS NULL ORDER BY score DESC, id LIMIT 1)`)
	fmt.Fprintf(w, `,best_benchmark_result_id=(SELECT id FROM benchmark_results r WHERE r.team_id=t.id AND r.finished AND r.exit_status=0 AND r.exit_signal IS NULL AND r.marked_at<'%s' ORDER BY score DESC,id LIMIT 1)
,best_benchmark_result_for_audience_id=(SELECT id FROM benchmark_results r WHERE r.team_id=t.id AND r.finished AND r.exit_status=0 AND r.exit_signal IS NULL AND r.marked_at<'%s' ORDER BY score DESC,id LIMIT 1)
WHERE t.id IN (SELECT team_id FROM fixture_scores);
COMMIT;
ANALYZE benchmark_results;
`, d.Config.Start.Add(d.Config.Duration).UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), d.freeze().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return w.Flush()
}
