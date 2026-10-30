// Package job は非同期ジョブ 20 本（FR-J）を実装する。
// 本番は PostgreSQL の jobs テーブル（cmd/worker が消費）、テストモードでは同期実行（perform_now 相当）。
package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/isucon/isucon2026/portal/internal/cache"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/external"
)

// Env はジョブが使う依存の束。
type Env struct {
	DB      *sql.DB
	Cfg     *config.Config
	Cache   *cache.Cache
	Slack   *external.Slack
	Discord *external.Discord
	GitHub  *external.GitHub
	Push    *external.WebPush
	AWS     *external.AWS
	SSH     *external.SSHExecutor

	queue Queue
}

// Queue は perform_later 相当の投入先。
type Queue interface {
	Enqueue(ctx context.Context, name string, args any) error
}

func (e *Env) SetQueue(q Queue) { e.queue = q }

// Enqueue は perform_later 相当。失敗はログに残すだけで呼び出し元へは返さない
// （Rails の perform_later と同じ非同期意味論）。
func (e *Env) Enqueue(ctx context.Context, name string, args any) {
	if err := e.queue.Enqueue(ctx, name, args); err != nil {
		slog.Error("job enqueue failed", "job", name, "error", err)
	}
}

func (e *Env) timing(ctx context.Context) config.Timing {
	return contest.TimingFromContext(ctx, e.Cfg.Timing)
}

// Perform はジョブ名でディスパッチする（cmd/worker と同期キューの共通経路）。
func (e *Env) Perform(ctx context.Context, name string, rawArgs json.RawMessage) error {
	decode := func(dst any) error {
		if len(rawArgs) == 0 {
			return nil
		}
		return json.Unmarshal(rawArgs, dst)
	}
	switch name {
	case "benchmark_completion":
		var a BenchmarkCompletionArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.BenchmarkCompletion(ctx, a)
	case "maintain_best_benchmark_result_of_team":
		var a TeamArgs
		if err := decode(&a); err != nil {
			return err
		}
		return contest.MaintainBestForTeam(ctx, e.DB, e.timing(ctx), a.TeamID, nil)
	case "update_contestant_dashboard":
		var a UpdateDashboardArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.UpdateContestantDashboard(ctx, a)
	case "clarification_acceptance":
		var a ClarificationAcceptanceArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.ClarificationAcceptance(ctx, a)
	case "clarification_response":
		var a ClarificationResponseArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.ClarificationResponse(ctx, a)
	case "deliver_push_notification":
		var a DeliverPushArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.DeliverPushNotification(ctx, a)
	case "discord_announce":
		var a TextArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.DiscordAnnounce(ctx, a.Text)
	case "slack_webhook":
		var a TextArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.Slack.Post(ctx, a.Text)
	case "fire_test_notification":
		var a ContestantArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.FireTestNotification(ctx, a.ContestantID)
	case "sync_ssh_keys_of_contestant":
		var a SyncSSHKeysArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.SyncSSHKeysOfContestant(ctx, a.ContestantID, a.AccessToken)
	case "sync_ssh_keys_of_all_contestants":
		var a SyncSSHKeysArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.SyncSSHKeysOfAllContestants(ctx, a.AccessToken)
	case "sync_discord_member_state_of_contestant":
		var a ContestantArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.SyncDiscordMemberState(ctx, a.ContestantID)
	case "sync_discord_member_state_of_all_contestants":
		return e.SyncDiscordMemberStateOfAll(ctx)
	case "maintain_discord_contestant_roles":
		var a MaintainRolesArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.MaintainDiscordContestantRoles(ctx, a)
	case "sync_contestant_instances":
		var a SyncInstancesArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.SyncContestantInstances(ctx, a.Project)
	case "execute_on_contestant_instance":
		var a ExecuteOnInstanceArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.ExecuteOnContestantInstance(ctx, a.RequestResultID)
	case "execute_on_multiple_contestant_instances":
		var a ExecuteOnMultipleArgs
		if err := decode(&a); err != nil {
			return err
		}
		return e.ExecuteOnMultipleContestantInstances(ctx, a.TeamIDs, a.Command, a.IncludeRouter)
	case "long_running_check":
		return e.LongRunningCheck(ctx)
	case "clear_contest_data":
		return e.ClearContestData(ctx)
	default:
		return fmt.Errorf("unknown job: %s", name)
	}
}

// InProcessQueue は DB を使わないキュー。
// Sync=true（テストモード）では同一コンテキストで即時実行し、
// それ以外はバックグラウンド goroutine で実行する。
type InProcessQueue struct {
	Env  *Env
	Sync bool
}

func (q *InProcessQueue) Enqueue(ctx context.Context, name string, args any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if q.Sync {
		if err := q.Env.Perform(ctx, name, raw); err != nil {
			slog.Error("job failed", "job", name, "error", err)
			return err
		}
		return nil
	}
	timing := contest.TimingFromContext(ctx, q.Env.Cfg.Timing)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("job panicked", "job", name, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		jctx, cancel := context.WithTimeout(contest.WithTiming(context.Background(), timing), 10*time.Minute)
		defer cancel()
		if err := q.Env.Perform(jctx, name, raw); err != nil {
			slog.Error("job failed", "job", name, "error", err)
		}
	}()
	return nil
}
