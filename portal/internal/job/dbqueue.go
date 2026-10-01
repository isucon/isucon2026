package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/isucon/isucon2026/portal/internal/contest"
)

const (
	// dbQueueMaxAttempts 回失敗したジョブは dead に落として再配信を止める。
	dbQueueMaxAttempts = 5
	// dbQueueLockTTL は claim したワーカーが死んだときに他ワーカーが拾い直せるまでの猶予。
	// ジョブ 1 件のタイムアウト（dbQueueJobTimeout）より長くする。
	dbQueueLockTTL    = 15 * time.Minute
	dbQueueJobTimeout = 10 * time.Minute
	// dbQueueBaseBackoff は失敗時の再試行間隔の初期値（失敗回数ごとに倍にする）。
	dbQueueBaseBackoff = 5 * time.Second
	dbQueueMaxBackoff  = 5 * time.Minute
	// DefaultPollInterval はキューが空だったときの次回ポーリングまでの間隔。
	DefaultPollInterval = time.Second

	jobStatusQueued  = "queued"
	jobStatusRunning = "running"
	jobStatusDead    = "dead"
)

// DBQueue は PostgreSQL の jobs テーブルをキューにする（SQS の置き換え）。
// 投入は INSERT、取得は FOR UPDATE SKIP LOCKED で 1 件ずつ排他的に claim する。
// 成功したジョブは行を消し、失敗したジョブは指数バックオフで queued に戻す。
type DBQueue struct {
	DB *sql.DB
}

// ClaimedJob は claim 済みの 1 件。Complete か Fail で必ず決着させる。
type ClaimedJob struct {
	ID       int64
	Name     string
	Args     json.RawMessage
	Attempts int
}

// Enqueue は perform_later 相当。同一トランザクション外なので、
// 呼び出し元のコミット前にワーカーが拾う可能性はあるが、
// 個々のジョブは対象行を DB から読み直すため副作用は再試行で収束する。
func (q *DBQueue) Enqueue(ctx context.Context, name string, args any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	_, err = q.DB.ExecContext(ctx, `INSERT INTO jobs (name, args) VALUES ($1, $2)`, name, raw)
	return err
}

// Claim は実行可能なジョブを 1 件取り出して running にする。無ければ nil を返す。
// locked_until を過ぎた running（ワーカー死亡）も拾い直す。
func (q *DBQueue) Claim(ctx context.Context) (*ClaimedJob, error) {
	j := &ClaimedJob{}
	err := q.DB.QueryRowContext(ctx, `
		UPDATE jobs SET status = $1, locked_until = now() + $2::interval, attempts = attempts + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM jobs
			WHERE (status = $3 AND run_at <= now()) OR (status = $1 AND locked_until < now())
			ORDER BY run_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, name, args, attempts`,
		jobStatusRunning, dbQueueLockTTL, jobStatusQueued,
	).Scan(&j.ID, &j.Name, &j.Args, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

// Complete は成功したジョブを消す。
func (q *DBQueue) Complete(ctx context.Context, id int64) error {
	_, err := q.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id = $1`, id)
	return err
}

// Fail は失敗したジョブをバックオフ付きで queued に戻す。上限に達したら dead にする。
func (q *DBQueue) Fail(ctx context.Context, j *ClaimedJob, cause error) error {
	msg := cause.Error()
	if j.Attempts >= dbQueueMaxAttempts {
		_, err := q.DB.ExecContext(ctx,
			`UPDATE jobs SET status = $1, locked_until = NULL, last_error = $2, updated_at = now() WHERE id = $3`,
			jobStatusDead, msg, j.ID)
		return err
	}
	_, err := q.DB.ExecContext(ctx, `
		UPDATE jobs SET status = $1, run_at = now() + $2::interval, locked_until = NULL, last_error = $3, updated_at = now()
		WHERE id = $4`,
		jobStatusQueued, backoff(j.Attempts), msg, j.ID)
	return err
}

// backoff は attempts 回目の失敗後に待つ時間（5s, 10s, 20s, ... 上限 5m）。
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := dbQueueBaseBackoff << (attempts - 1)
	if d > dbQueueMaxBackoff || d <= 0 {
		return dbQueueMaxBackoff
	}
	return d
}

// Work は 1 件 claim して実行し、決着させる。処理したジョブがあれば true。
// ジョブの panic は失敗として扱い、ワーカーは落とさない。
func (q *DBQueue) Work(ctx context.Context, env *Env) (bool, error) {
	j, err := q.Claim(ctx)
	if err != nil {
		return false, err
	}
	if j == nil {
		return false, nil
	}

	jctx, cancel := context.WithTimeout(contest.WithTiming(ctx, env.Cfg.Timing), dbQueueJobTimeout)
	perr := performSafely(jctx, env, j)
	cancel()

	if perr != nil {
		slog.Error("job failed", "job", j.Name, "id", j.ID, "attempts", j.Attempts, "error", perr)
		return true, q.Fail(ctx, j, perr)
	}
	return true, q.Complete(ctx, j.ID)
}

func performSafely(ctx context.Context, env *Env, j *ClaimedJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("job panicked", "job", j.Name, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return env.Perform(ctx, j.Name, j.Args)
}

// Run は ctx が終わるまでジョブを処理し続ける（cmd/worker のメインループ）。
// キューが空のときだけ interval 待ち、それ以外は連続して claim する。
func (q *DBQueue) Run(ctx context.Context, env *Env, interval time.Duration) {
	for {
		worked, err := q.Work(ctx, env)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Error("job queue error", "error", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
