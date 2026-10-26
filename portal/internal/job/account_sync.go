package job

import (
	"context"
	"database/sql"
	"errors"
)

// ScheduleAccountSync starts an hourly reconciliation cycle. The durable gate
// and job insertion commit together, so restarts and concurrent workers cannot
// enqueue the same cycle twice. Pending work is retained rather than duplicated.
func (q *DBQueue) ScheduleAccountSync(ctx context.Context, discordEnabled bool) error {
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	err = tx.QueryRowContext(ctx, `
  INSERT INTO periodic_job_schedules(name,next_run_at)
  VALUES('account_sync',now()+interval '1 hour')
  ON CONFLICT(name) DO UPDATE SET next_run_at=now()+interval '1 hour'
  WHERE periodic_job_schedules.next_run_at <= now()
  RETURNING name`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Spread individual jobs across the hour to avoid a burst of external API
	// requests and keep the worker available for interactive jobs. GitHub uses
	// public .keys URLs; no user OAuth tokens are stored by this scheduler.
	_, err = tx.ExecContext(ctx, `
  WITH targets AS (
   SELECT c.id, j.name FROM contestants c JOIN teams t ON t.id=c.team_id
   CROSS JOIN (VALUES ('sync_ssh_keys_of_contestant'),
                      ('sync_discord_member_state_of_contestant'),
                      ('maintain_discord_contestant_roles')) AS j(name)
   WHERE ((j.name='sync_ssh_keys_of_contestant' AND c.github_login<>'' AND NOT t.withdrawn AND NOT t.disqualified)
    OR (j.name<>'sync_ssh_keys_of_contestant' AND $1 AND c.discord_id<>''))
   AND NOT EXISTS (
    SELECT 1 FROM jobs pending WHERE pending.name=j.name
     AND pending.args->>'contestant_id'=c.id::text AND pending.status IN ('queued','running')
   )
  )
  INSERT INTO jobs(name,args,run_at)
  SELECT name,jsonb_build_object('contestant_id',id),
   now()+interval '1 hour' * ((row_number() OVER (ORDER BY id,name)-1)::double precision / count(*) OVER ())
  FROM targets`, discordEnabled)
	if err != nil {
		return err
	}
	return tx.Commit()
}
