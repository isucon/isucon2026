package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

// PostgreSQL ジョブキュー（SQS の置き換え）: 投入 → claim → 完了で行が消える。
func TestDBQueueEnqueueClaimComplete(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	q := &job.DBQueue{DB: conn}

	if err := q.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	claimed, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("expected a job to be claimed")
	}
	if claimed.Name != "slack_webhook" || claimed.Attempts != 1 {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	// jsonb は空白を正規化するので、デコードして比較する
	var args job.TextArgs
	if err := json.Unmarshal(claimed.Args, &args); err != nil {
		t.Fatalf("args %s: %v", claimed.Args, err)
	}
	if args.Text != "hello" {
		t.Fatalf("args = %s", claimed.Args)
	}

	// claim 中は他のワーカーから見えない
	if again, err := q.Claim(ctx); err != nil || again != nil {
		t.Fatalf("running job must not be claimed twice: job=%v err=%v", again, err)
	}

	if err := q.Complete(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("completed job must be deleted, got %d rows", n)
	}
}

// 失敗したジョブはバックオフ付きで queued に戻り、すぐには再 claim されない。上限で dead。
func TestDBQueueFailRequeuesWithBackoffThenDies(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	q := &job.DBQueue{DB: conn}

	if err := q.Enqueue(ctx, "slack_webhook", nil); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.Claim(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("claim: job=%v err=%v", claimed, err)
	}
	if err := q.Fail(ctx, claimed, errors.New("boom")); err != nil {
		t.Fatal(err)
	}

	var status, lastError string
	var runAt time.Time
	if err := conn.QueryRowContext(ctx, `SELECT status, last_error, run_at FROM jobs WHERE id = $1`, claimed.ID).
		Scan(&status, &lastError, &runAt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || lastError != "boom" {
		t.Fatalf("status=%s last_error=%s", status, lastError)
	}
	if !runAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("run_at must be pushed into the future, got %v", runAt)
	}
	if again, err := q.Claim(ctx); err != nil || again != nil {
		t.Fatalf("backoff must hold the job back: job=%v err=%v", again, err)
	}

	// 上限回数まで失敗させると dead になり claim されない
	for attempts := 2; attempts <= 5; attempts++ {
		if _, err := conn.ExecContext(ctx, `UPDATE jobs SET run_at = now() WHERE id = $1`, claimed.ID); err != nil {
			t.Fatal(err)
		}
		c, err := q.Claim(ctx)
		if err != nil || c == nil {
			t.Fatalf("attempt %d: job=%v err=%v", attempts, c, err)
		}
		if c.Attempts != attempts {
			t.Fatalf("attempts = %d, want %d", c.Attempts, attempts)
		}
		if err := q.Fail(ctx, c, errors.New("boom")); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = $1`, claimed.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "dead" {
		t.Fatalf("status = %s, want dead", status)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE jobs SET run_at = now() WHERE id = $1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := q.Claim(ctx); err != nil || c != nil {
		t.Fatalf("dead job must not be claimed: job=%v err=%v", c, err)
	}
}

// ワーカーが死んでロックが切れた running ジョブは拾い直される。
func TestDBQueueReclaimsExpiredLock(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	q := &job.DBQueue{DB: conn}

	if err := q.Enqueue(ctx, "slack_webhook", nil); err != nil {
		t.Fatal(err)
	}
	first, err := q.Claim(ctx)
	if err != nil || first == nil {
		t.Fatalf("claim: job=%v err=%v", first, err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE jobs SET locked_until = now() - interval '1 second' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := q.Claim(ctx)
	if err != nil || second == nil {
		t.Fatalf("expired lock must be reclaimed: job=%v err=%v", second, err)
	}
	if second.ID != first.ID || second.Attempts != 2 {
		t.Fatalf("unexpected reclaim: %+v", second)
	}
}

// Work は Perform を経由してジョブを決着させる（成功は削除、未知ジョブは失敗として再試行に回る）。
func TestDBQueueWorkPerformsThroughEnv(t *testing.T) {
	p := testkit.Start(t)
	ctx := context.Background()
	q := &job.DBQueue{DB: p.DB}

	// Slack webhook URL が空なら Post は no-op で成功する
	if err := q.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ctx, "no_such_job", nil); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		worked, err := q.Work(ctx, p.Jobs)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			t.Fatal("expected a job to be worked")
		}
	}
	if worked, err := q.Work(ctx, p.Jobs); err != nil || worked {
		t.Fatalf("queue should be drained: worked=%v err=%v", worked, err)
	}

	var names []string
	rows, err := p.DB.QueryContext(ctx, `SELECT name FROM jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if len(names) != 1 || names[0] != "no_such_job" {
		t.Fatalf("only the failed job must remain, got %v", names)
	}
}
