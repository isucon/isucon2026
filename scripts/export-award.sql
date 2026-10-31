-- Read-only export. Pass contest_start, scoreboard_close, contest_end with psql -v.
WITH timing AS (
    SELECT :'contest_start'::timestamptz AS start_at,
           :'scoreboard_close'::timestamptz AS close_at,
           :'contest_end'::timestamptz AS end_at
), eligible_teams AS (
    SELECT id, name FROM teams
    WHERE NOT disqualified AND NOT withdrawn AND NOT is_hidden
), runs AS (
    SELECT r.id, r.team_id, j.created_at AS enqueued_at, r.marked_at AS at,
           CASE WHEN r.passed = false THEN 0 ELSE r.score END AS score,
           COALESCE(r.passed, true) AS passed
    FROM benchmark_results r
    JOIN benchmark_jobs j ON j.id = r.benchmark_job_id AND j.team_id = r.team_id
    JOIN eligible_teams t ON t.id = r.team_id
    CROSS JOIN timing
    WHERE r.finished AND r.exit_status = 0 AND r.exit_signal IS NULL
      AND j.post_validation IS NOT TRUE
      AND r.marked_at >= timing.start_at AND r.marked_at < timing.end_at
), latest AS (
    SELECT DISTINCT ON (team_id) * FROM runs
    ORDER BY team_id, at DESC, id DESC
), ranks AS (
    SELECT team_id, row_number() OVER (ORDER BY score DESC, at ASC, team_id ASC) AS final_rank
    FROM latest WHERE passed
), members AS (
    SELECT team_id, count(*) AS member_count FROM contestants GROUP BY team_id
), histories AS (
    SELECT team_id, jsonb_agg(jsonb_build_object(
        'enqueued_at', enqueued_at, 'at', at, 'score', score, 'passed', passed
    ) ORDER BY at, id) AS runs
    FROM runs GROUP BY team_id
)
SELECT jsonb_build_object(
    'contest_started_at', timing.start_at,
    'scoreboard_closed_at', timing.close_at,
    'contest_ended_at', timing.end_at,
    'teams', COALESCE((SELECT jsonb_agg(jsonb_build_object(
        'id', t.id, 'name', t.name, 'member_count', COALESCE(m.member_count, 0),
        'final_rank', r.final_rank, 'disqualified', false,
        'runs', COALESCE(h.runs, '[]'::jsonb)
    ) ORDER BY t.id)
    FROM eligible_teams t
    LEFT JOIN ranks r ON r.team_id = t.id
    LEFT JOIN members m ON m.team_id = t.id
    LEFT JOIN histories h ON h.team_id = t.id), '[]'::jsonb)
) FROM timing;
