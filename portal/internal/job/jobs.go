package job

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	pbaudience "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/audience"
	pbcontestant "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/contestant"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/model"
)

type BenchmarkCompletionArgs struct {
	JobID int64 `json:"job_id"`
}
type TeamArgs struct {
	TeamID int64 `json:"team_id"`
}
type ContestantArgs struct {
	ContestantID int64 `json:"contestant_id"`
}
type TextArgs struct {
	Text string `json:"text"`
}
type UpdateDashboardArgs struct {
	TeamID int64      `json:"team_id"` // 0 = 全チーム
	Frozen bool       `json:"frozen"`
	Now    *time.Time `json:"now,omitempty"`
}
type ClarificationAcceptanceArgs struct {
	ClarificationID int64 `json:"clarification_id"`
}
type ClarificationResponseArgs struct {
	ClarificationID int64 `json:"clarification_id"`
	WasAnswered     bool  `json:"was_answered"`
}
type DeliverPushArgs struct {
	NotificationID int64 `json:"notification_id"`
}
type SyncSSHKeysArgs struct {
	ContestantID int64  `json:"contestant_id,omitempty"`
	AccessToken  string `json:"access_token"`
}
type MaintainRolesArgs struct {
	ContestantID   int64  `json:"contestant_id,omitempty"`
	ForceDiscordID string `json:"force_discord_id,omitempty"`
}
type SyncInstancesArgs struct {
	Project string `json:"project"`
}
type ExecuteOnInstanceArgs struct {
	RequestResultID int64 `json:"request_result_id"`
}
type ExecuteOnMultipleArgs struct {
	IncludeRouter bool    `json:"include_router,omitempty"`
	TeamIDs       []int64 `json:"team_ids"`
	Command       string  `json:"command"`
}

// BenchmarkCompletion は BenchmarkCompletionJob 相当。
// best 更新 → ダッシュボード再生成 → Web Push → Slack（エラー時）。
func (e *Env) BenchmarkCompletion(ctx context.Context, a BenchmarkCompletionArgs) error {
	timing := e.timing(ctx)
	job, err := model.FindJob(ctx, e.DB, a.JobID)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	team, err := model.FindTeam(ctx, e.DB, job.TeamID)
	if err != nil || team == nil {
		return err
	}

	if err := contest.MaintainBestForTeam(ctx, e.DB, timing, team.ID, nil); err != nil {
		return err
	}

	frozenAt := time.Now().UTC()
	if job.FinishedAt.Valid {
		frozenAt = job.FinishedAt.Time
	}
	if err := e.UpdateContestantDashboard(ctx, UpdateDashboardArgs{
		TeamID: team.ID,
		Frozen: contest.ContestFrozen(timing, frozenAt),
	}); err != nil {
		return err
	}

	// Web Push（競技時間内のみ）
	open, err := contest.ContestOpenForTeam(ctx, e.DB, timing, team.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	if open {
		members, err := model.TeamMembers(ctx, e.DB, team.ID)
		if err != nil {
			return err
		}
		for _, m := range members {
			n, err := model.CreateNotification(ctx, e.DB, m.ID, &pbresources.Notification{
				Content: &pbresources.Notification_ContentBenchmarkJob{
					ContentBenchmarkJob: &pbresources.Notification_BenchmarkJobMessage{BenchmarkJobId: job.ID},
				},
			})
			if err != nil {
				return err
			}
			e.Enqueue(ctx, "deliver_push_notification", DeliverPushArgs{NotificationID: n.ID})
		}
	}

	// Slack（エラー時のみ）
	if job.Status == model.JobErrored {
		lines := []string{
			fmt.Sprintf(":hourglass: Benchmark Job #%d completed for %s (#%d) <https://%s/admin/benchmark_jobs/%d|Open>",
				job.ID, team.Name, team.ID, e.Cfg.PublicURLHost, job.ID),
			":warning: *ERRORED*",
		}
		if result, err := model.FindResultByJob(ctx, e.DB, job.ID); err == nil && result != nil {
			passed := "Failed"
			if result.Passed.Valid && result.Passed.Bool {
				passed = "Passed"
			}
			lines = append(lines, "", fmt.Sprintf("> *%s, score=%d* (raw=%d,deduction=%d)", passed, result.Score, result.ScoreRaw, result.ScoreDeduction))
			if result.Reason.Valid && result.Reason.String != "" {
				lines = append(lines, "> reason="+result.Reason.String)
			}
			if !result.SuccessfullyExited() {
				lines = append(lines, fmt.Sprintf("> exit_status=%v, signaled=%v, exit_signal=%v", result.ExitStatus.Int64, result.Signaled(), result.ExitSignal.Int64))
			}
		}
		if err := e.Slack.Post(ctx, strings.Join(lines, "\n")); err != nil {
			return err
		}
	}
	return nil
}

// UpdateContestantDashboard は UpdateContestantDashboardJob 相当。
// Redis に事前生成キャッシュを焼く（キーは cache パッケージのコメント参照）。
func (e *Env) UpdateContestantDashboard(ctx context.Context, a UpdateDashboardArgs) error {
	timing := e.timing(ctx)
	now := time.Now().UTC()

	lb, _, err := contest.BuildLeaderboard(ctx, e.DB, timing, now, contest.LeaderboardParams{History: false, Now: a.Now})
	if err != nil {
		return err
	}
	lbWire, err := proto.Marshal(lb)
	if err != nil {
		return err
	}

	audienceResp, err := proto.Marshal(&pbaudience.DashboardResponse{Leaderboard: lb})
	if err != nil {
		return err
	}
	if err := e.Cache.Write(ctx, "dashboard-v2:audience", audienceResp); err != nil {
		return err
	}

	if a.Frozen {
		adminLB, _, err := contest.BuildLeaderboard(ctx, e.DB, timing, now, contest.LeaderboardParams{Admin: true, History: false, Now: a.Now})
		if err != nil {
			return err
		}
		wire, err := proto.Marshal(adminLB)
		if err != nil {
			return err
		}
		if err := e.Cache.Write(ctx, "leaderboard-v2:admin", wire); err != nil {
			return err
		}
	} else {
		if err := e.Cache.Write(ctx, "leaderboard-v2:admin", lbWire); err != nil {
			return err
		}
	}

	var teamIDs []int64
	if a.TeamID != 0 {
		teamIDs = []int64{a.TeamID}
	} else {
		rows, err := e.DB.QueryContext(ctx, `SELECT id FROM teams WHERE withdrawn = false AND disqualified = false ORDER BY id ASC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			teamIDs = append(teamIDs, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	for _, teamID := range teamIDs {
		_, item, err := contest.BuildLeaderboard(ctx, e.DB, timing, now, contest.LeaderboardParams{
			Audience: true, TeamID: teamID, History: true, Solo: true, SoloItem: true, Now: a.Now,
		})
		if err != nil {
			return err
		}

		if wire, err := proto.Marshal(&pbaudience.SoloDashboardResponse{LeaderboardItem: item}); err != nil {
			return err
		} else if err := e.Cache.Write(ctx, fmt.Sprintf("dashboard-v2:audience:team-%d", teamID), wire); err != nil {
			return err
		}

		contestantItem := item
		adminItem := item
		if a.Frozen {
			_, contestantItem, err = contest.BuildLeaderboard(ctx, e.DB, timing, now, contest.LeaderboardParams{
				TeamID: teamID, History: true, Solo: true, SoloItem: true, Now: a.Now,
			})
			if err != nil {
				return err
			}
			_, adminItem, err = contest.BuildLeaderboard(ctx, e.DB, timing, now, contest.LeaderboardParams{
				Admin: true, TeamID: teamID, History: true, Solo: true, SoloItem: true, Now: a.Now,
			})
			if err != nil {
				return err
			}
		}

		if wire, err := proto.Marshal(&pbcontestant.DashboardResponse{LeaderboardItem: contestantItem}); err != nil {
			return err
		} else if err := e.Cache.Write(ctx, fmt.Sprintf("dashboard-v2:contestant:team-%d", teamID), wire); err != nil {
			return err
		}

		if wire, err := proto.Marshal(&pbadmin.SoloDashboardResponse{LeaderboardItem: adminItem}); err != nil {
			return err
		} else if err := e.Cache.Write(ctx, fmt.Sprintf("dashboard-v2:admin:team-%d", teamID), wire); err != nil {
			return err
		}
	}
	return nil
}

// ClarificationAcceptance は選手の質問投稿を Slack へ通知する。
func (e *Env) ClarificationAcceptance(ctx context.Context, a ClarificationAcceptanceArgs) error {
	clar, err := model.FindClarification(ctx, e.DB, a.ClarificationID)
	if err != nil || clar == nil {
		return err
	}
	teamName := ""
	if clar.TeamID.Valid {
		if team, err := model.FindTeam(ctx, e.DB, clar.TeamID.Int64); err == nil && team != nil {
			teamName = team.Name
		}
	}
	return e.Slack.Post(ctx, fmt.Sprintf(":mailbox_with_mail: Clarification request #%d from %s (#%d) <https://%s/admin/clarifications/%d|Open>",
		clar.ID, teamName, clar.TeamID.Int64, e.Cfg.PublicURLHost, clar.ID))
}

// ClarificationResponse は ClarificationResponseJob 相当。
// Discord アナウンス + 全選手への通知一括挿入（NF-4）+ 当該チームへの Push。
func (e *Env) ClarificationResponse(ctx context.Context, a ClarificationResponseArgs) error {
	clar, err := model.FindClarification(ctx, e.DB, a.ClarificationID)
	if err != nil || clar == nil {
		return err
	}

	if clar.IsDisclosed() {
		var announce string
		switch {
		case clar.Admin && !a.WasAnswered:
			announce = fmt.Sprintf(":loudspeaker: 新しいアナウンスが投稿されました (#%d) https://%s/contestant/clarifications", clar.ID, e.Cfg.PublicURLHost)
		case clar.Admin && a.WasAnswered:
			announce = fmt.Sprintf(":loudspeaker: アナウンスが更新されました (#%d) https://%s/contestant/clarifications", clar.ID, e.Cfg.PublicURLHost)
		case !a.WasAnswered:
			announce = fmt.Sprintf(":loudspeaker: 新しい質問と回答が公開されました (#%d) https://%s/contestant/clarifications", clar.ID, e.Cfg.PublicURLHost)
		default:
			announce = fmt.Sprintf(":loudspeaker: 質問と回答が更新されました (#%d) https://%s/contestant/clarifications", clar.ID, e.Cfg.PublicURLHost)
		}
		if err := e.DiscordAnnounce(ctx, announce); err != nil {
			slog.Error("discord announce failed", "error", err)
		}

		encoded, err := model.EncodeNotificationMessage(&pbresources.Notification{
			Content: &pbresources.Notification_ContentClarification{
				ContentClarification: &pbresources.Notification_ClarificationMessage{
					ClarificationId: clar.ID,
					Updated:         a.WasAnswered,
					Admin:           clar.Admin,
					Owned:           false,
				},
			},
		})
		if err != nil {
			return err
		}

		query := `SELECT contestants.id FROM contestants JOIN teams ON teams.id = contestants.team_id
			WHERE teams.withdrawn = false AND teams.disqualified = false`
		args := []any{}
		if clar.TeamID.Valid {
			query += ` AND contestants.team_id != $1`
			args = append(args, clar.TeamID.Int64)
		}
		rows, err := e.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := model.InsertNotificationsBatch(ctx, e.DB, ids, encoded, clar.UpdatedAt); err != nil {
			return err
		}
	}

	if clar.TeamID.Valid {
		members, err := model.TeamMembers(ctx, e.DB, clar.TeamID.Int64)
		if err != nil {
			return err
		}
		for _, m := range members {
			n, err := model.CreateNotification(ctx, e.DB, m.ID, &pbresources.Notification{
				Content: &pbresources.Notification_ContentClarification{
					ContentClarification: &pbresources.Notification_ClarificationMessage{
						ClarificationId: clar.ID,
						Updated:         a.WasAnswered,
						Admin:           clar.Admin,
						Owned:           true,
					},
				},
			})
			if err != nil {
				return err
			}
			e.Enqueue(ctx, "deliver_push_notification", DeliverPushArgs{NotificationID: n.ID})
		}
	}
	return nil
}

// DeliverPushNotification は DeliverPushNotificationJob 相当。
// ペイロードは base64(Notification の protobuf バイナリ)（CT-4）。410/404 で購読を自動削除。
func (e *Env) DeliverPushNotification(ctx context.Context, a DeliverPushArgs) error {
	rows, err := model.QueryNotifications(ctx, e.DB,
		`SELECT notifications.id, notifications.contestant_id, notifications."read", notifications.encoded_message, notifications.created_at FROM notifications WHERE id = $1`, a.NotificationID)
	if err != nil || len(rows) == 0 {
		return err
	}
	n := rows[0]

	pb, err := n.ToPB()
	if err != nil {
		return err
	}
	wire, err := proto.Marshal(pb)
	if err != nil {
		return err
	}
	payload, err := model.EncodeNotificationMessage(pb)
	_ = wire
	if err != nil {
		return err
	}

	subs, err := model.ContestantPushSubscriptions(ctx, e.DB, n.ContestantID)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		err := e.Push.Send(ctx, sub.Endpoint, sub.P256dh, sub.Auth, payload)
		if err == external.ErrSubscriptionGone {
			if _, derr := e.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, sub.ID); derr != nil {
				return derr
			}
			continue
		}
		if err != nil {
			slog.Warn("webpush failed", "subscription_id", sub.ID, "error", err)
		}
	}
	return nil
}

// DiscordAnnounce はアナウンスチャンネルへの投稿。
func (e *Env) DiscordAnnounce(ctx context.Context, text string) error {
	return e.Discord.CreateMessage(ctx, e.Cfg.Discord.ChannelID, text)
}

// FireTestNotification はテスト通知を作って即時配信する。
func (e *Env) FireTestNotification(ctx context.Context, contestantID int64) error {
	n, err := model.CreateNotification(ctx, e.DB, contestantID, &pbresources.Notification{
		Content: &pbresources.Notification_ContentTest{
			ContentTest: &pbresources.Notification_TestMessage{Something: rand.Int63n(100)},
		},
	})
	if err != nil {
		return err
	}
	return e.DeliverPushNotification(ctx, DeliverPushArgs{NotificationID: n.ID})
}

// SyncSSHKeysOfContestant は GitHub から公開鍵を取得して差分同期する。
func (e *Env) SyncSSHKeysOfContestant(ctx context.Context, contestantID int64, accessToken string) error {
	c, err := model.FindContestant(ctx, e.DB, contestantID)
	if err != nil || c == nil {
		return err
	}
	keys, err := e.GitHub.FetchSSHKeys(ctx, c.GitHubLogin, accessToken)
	if err != nil {
		return err
	}

	rows, err := e.DB.QueryContext(ctx, `SELECT id, public_key FROM ssh_public_keys WHERE contestant_id = $1`, c.ID)
	if err != nil {
		return err
	}
	existing := map[string]int64{}
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return err
		}
		existing[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, ok := existing[key]; ok {
			delete(existing, key)
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ssh_public_keys (contestant_id, public_key, created_at, updated_at) VALUES ($1, $2, now(), now())`,
			c.ID, key); err != nil {
			return err
		}
	}
	for _, id := range existing {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ssh_public_keys WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncSSHKeysOfAllContestants は全選手の鍵を同期する（運営の一括実行）。
func (e *Env) SyncSSHKeysOfAllContestants(ctx context.Context, accessToken string) error {
	rows, err := e.DB.QueryContext(ctx, `SELECT id FROM contestants ORDER BY id DESC`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := e.SyncSSHKeysOfContestant(ctx, id, accessToken); err != nil {
			slog.Warn("ssh key sync failed", "contestant_id", id, "error", err)
		}
	}
	return nil
}

// SyncDiscordMemberState は Discord ギルド参加状態を同期する。
func (e *Env) SyncDiscordMemberState(ctx context.Context, contestantID int64) error {
	c, err := model.FindContestant(ctx, e.DB, contestantID)
	if err != nil || c == nil {
		return err
	}
	isMember := true
	if _, err := e.Discord.GetMember(ctx, c.DiscordID); err == external.ErrDiscordNotFound {
		isMember = false
	} else if err != nil {
		return err
	}
	_, err = e.DB.ExecContext(ctx,
		`UPDATE contestants SET is_discord_guild_member = $1, updated_at = now() WHERE id = $2`, isMember, c.ID)
	return err
}

// SyncDiscordMemberStateOfAll は全選手の参加状態を一括同期する。
func (e *Env) SyncDiscordMemberStateOfAll(ctx context.Context) error {
	members, err := e.Discord.ListMembers(ctx, 1000)
	if err != nil {
		return err
	}
	inGuild := make(map[string]bool, len(members))
	for _, m := range members {
		inGuild[m.User.ID] = true
	}
	rows, err := e.DB.QueryContext(ctx, `SELECT id, discord_id FROM contestants`)
	if err != nil {
		return err
	}
	type pair struct {
		id     int64
		member bool
	}
	var updates []pair
	for rows.Next() {
		var id int64
		var discordID string
		if err := rows.Scan(&id, &discordID); err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, pair{id, inGuild[discordID]})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := e.DB.ExecContext(ctx, `UPDATE contestants SET is_discord_guild_member = $1 WHERE id = $2`, u.member, u.id); err != nil {
			return err
		}
	}
	return nil
}

// MaintainDiscordContestantRoles はロールの付与・剥奪とニックネーム設定。
// 単一開催（P-3）につき contestant ロール 1 系統のみを管理する。
func (e *Env) MaintainDiscordContestantRoles(ctx context.Context, a MaintainRolesArgs) error {
	basicRole := e.Cfg.Discord.ContestantRoleID
	rolesToManage := []string{basicRole}

	var c *model.Contestant
	var err error
	if a.ContestantID != 0 {
		c, err = model.FindContestant(ctx, e.DB, a.ContestantID)
		if err != nil {
			return err
		}
	}
	userID := a.ForceDiscordID
	if userID == "" && c != nil {
		userID = c.DiscordID
	}
	if userID == "" {
		return nil
	}

	var desired []string
	var team *model.Team
	if c != nil {
		team, err = model.FindTeam(ctx, e.DB, c.TeamID)
		if err != nil {
			return err
		}
		if team != nil && team.Active() {
			desired = []string{basicRole}
		}
	}

	member, err := e.Discord.GetMember(ctx, userID)
	if err == external.ErrDiscordNotFound {
		slog.Warn("discord member not found", "discord_id", userID)
		return nil
	}
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, r := range member.Roles {
		present[r] = true
	}
	desiredSet := map[string]bool{}
	for _, r := range desired {
		desiredSet[r] = true
	}

	addedBasic := false
	for _, r := range desired {
		if !present[r] {
			if err := e.Discord.AddMemberRole(ctx, userID, r); err != nil {
				return err
			}
			if r == basicRole {
				addedBasic = true
			}
		}
	}
	for _, r := range rolesToManage {
		if present[r] && !desiredSet[r] {
			if err := e.Discord.RemoveMemberRole(ctx, userID, r); err != nil {
				return err
			}
		}
	}

	if c != nil && team != nil && !team.IsHidden && addedBasic {
		nick := fmt.Sprintf("%s (%s)", c.Name, team.Name)
		if runes := []rune(nick); len(runes) > 32 {
			nick = string(runes[:32])
		}
		if err := e.Discord.UpdateMemberNick(ctx, c.DiscordID, nick); err != nil {
			return err
		}
	}
	return nil
}

// SyncContestantInstances は EC2 タグ（IsuconTeamID / IsuconInstanceNum）で同期する。
func (e *Env) SyncContestantInstances(ctx context.Context, project string) error {
	if e.AWS == nil {
		slog.Info("sync_contestant_instances (skipped: no aws)")
		return nil
	}
	instances, err := e.AWS.ListContestantInstances(ctx, project)
	if err != nil {
		return err
	}
	return e.SyncEC2Instances(ctx, instances)
}

// ExecuteOnContestantInstance は 1 台への SSH コマンド実行と結果記録。
func (e *Env) ExecuteOnContestantInstance(ctx context.Context, requestResultID int64) (retErr error) {
	result, err := model.FindCommandRequestResult(ctx, e.DB, requestResultID)
	if err != nil || result == nil {
		return err
	}
	if result.RouterTeamID.Valid {
		defer func() {
			if err := e.enqueueTeamCommands(ctx, result.RequestID, result.RouterTeamID.Int64); err != nil && retErr == nil {
				retErr = err
			}
		}()
	}
	instance, err := model.FindInstance(ctx, e.DB, result.ContestantInstanceID)
	if err != nil {
		return err
	}
	address := ""
	jump := ""
	if instance != nil {
		address = instance.PublicIPv4Address.String
	}
	if result.RouterTeamID.Valid {
		router, err := model.FindTeamRouter(ctx, e.DB, result.RouterTeamID.Int64)
		if err != nil {
			return err
		}
		if router != nil {
			address = router.PrivateIP.String
			servers, err := model.TeamInstances(ctx, e.DB, result.RouterTeamID.Int64)
			if err != nil {
				return err
			}
			for _, server := range servers {
				if server.PublicIPv4Address.Valid {
					jump = server.PublicIPv4Address.String
					break
				}
			}
			if jump == "" {
				address = ""
			}
		}
	}
	if address == "" {
		_, err = e.DB.ExecContext(ctx, `UPDATE instance_command_execute_request_results SET output=$1, exit_code=-1, finished_at=now(), updated_at=now() WHERE id=$2`, "target has no public IP", result.ID)
		return err
	}
	request, err := model.FindCommandRequest(ctx, e.DB, result.RequestID)
	if err != nil || request == nil {
		return err
	}

	output, exitCode, runErr := e.SSH.RunVia(jump, address, request.Command.String)
	if runErr != nil {
		output = runErr.Error()
		exitCode = -1
	}

	_, err = e.DB.ExecContext(ctx, `
		UPDATE instance_command_execute_request_results
		SET output = $1, exit_code = $2, finished_at = now(), updated_at = now() WHERE id = $3`,
		output, exitCode, result.ID)
	return err
}

func (e *Env) enqueueTeamCommands(ctx context.Context, requestID, teamID int64) error {
	rows, err := e.DB.QueryContext(ctx, `SELECT r.id FROM instance_command_execute_request_results r JOIN contestant_instances i ON i.id=r.contestant_instance_id WHERE r.instance_command_execute_request_id=$1 AND i.team_id=$2 AND r.finished_at IS NULL`, requestID, teamID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := e.queue.Enqueue(ctx, "execute_on_contestant_instance", ExecuteOnInstanceArgs{RequestResultID: id}); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteOnMultipleContestantInstances は一括実行のリクエスト作成と各台の実行投入。
func (e *Env) ExecuteOnMultipleContestantInstances(ctx context.Context, teamIDs []int64, command string, includeRouter ...bool) error {
	var requestID int64
	if err := e.DB.QueryRowContext(ctx,
		`INSERT INTO instance_command_execute_requests (command, created_at, updated_at) VALUES ($1, now(), now()) RETURNING id`, command).Scan(&requestID); err != nil {
		return err
	}

	if len(teamIDs) == 0 {
		return nil
	}
	if _, err := e.DB.ExecContext(ctx, `
		INSERT INTO instance_command_execute_request_results (instance_command_execute_request_id, contestant_instance_id, created_at, updated_at)
		SELECT $1, id, now(), now() FROM contestant_instances WHERE team_id = ANY($2) ORDER BY team_id ASC`,
		requestID, teamIDs); err != nil {
		return err
	}

	if len(includeRouter) > 0 && includeRouter[0] {
		_, err := e.DB.ExecContext(ctx, `INSERT INTO instance_command_execute_request_results (instance_command_execute_request_id, router_team_id, created_at, updated_at)
 SELECT $1, id, now(), now() FROM teams WHERE id=ANY($2)`, requestID, teamIDs)
		if err != nil {
			return err
		}
	}

	results, err := model.ListCommandRequestResults(ctx, e.DB, requestID)
	if err != nil {
		return err
	}
	for _, r := range results {
		if len(includeRouter) > 0 && includeRouter[0] && !r.RouterTeamID.Valid {
			continue
		}
		e.Enqueue(ctx, "execute_on_contestant_instance", ExecuteOnInstanceArgs{RequestResultID: r.ID})
	}
	return nil
}

// LongRunningCheck は clar 2 分滞留・ベンチ作成から 5 分以上を Slack 警告する（notifyloop が 60 秒間隔で叩く）。
func (e *Env) LongRunningCheck(ctx context.Context) error {
	now := time.Now().UTC()
	var messages []string

	clars, err := model.QueryClarifications(ctx, e.DB, `
		SELECT clarifications.id, clarifications.team_id, clarifications.disclosed, clarifications.question, clarifications.answer, clarifications.original_question, clarifications.answered_at, clarifications.admin, clarifications.created_at, clarifications.updated_at
		FROM clarifications WHERE answered_at IS NULL AND admin = false AND created_at <= $1`, now.Add(-2*time.Minute))
	if err != nil {
		return err
	}
	for _, clar := range clars {
		teamName := ""
		if clar.TeamID.Valid {
			if team, err := model.FindTeam(ctx, e.DB, clar.TeamID.Int64); err == nil && team != nil {
				teamName = team.Name
			}
		}
		messages = append(messages, fmt.Sprintf(":warning: Remaining *unanswered clarification* #%d from %s (#%d), %s <https://%s/admin/clarifications/%d|Open>",
			clar.ID, teamName, clar.TeamID.Int64, humanizeSince(now, clar.CreatedAt), e.Cfg.PublicURLHost, clar.ID))
	}

	jobs, err := model.QueryJobs(ctx, e.DB, `
		SELECT `+"benchmark_jobs.id, benchmark_jobs.team_id, benchmark_jobs.status, benchmark_jobs.instance_name, benchmark_jobs.handle, benchmark_jobs.started_at, benchmark_jobs.finished_at, benchmark_jobs.created_at, benchmark_jobs.updated_at, benchmark_jobs.target_id, benchmark_jobs.enqueued_by_id, benchmark_jobs.post_validation"+`
		FROM benchmark_jobs WHERE status IN ($1, $2) AND created_at <= $3`,
		model.JobPending, model.JobRunning, now.Add(-5*time.Minute))
	if err != nil {
		return err
	}
	for _, job := range jobs {
		teamName := ""
		if team, err := model.FindTeam(ctx, e.DB, job.TeamID); err == nil && team != nil {
			teamName = team.Name
		}
		messages = append(messages, fmt.Sprintf(":warning: *Long running benchmark* job #%d _%s_ from %s (#%d), %s <https://%s/admin/benchmark_jobs/%d|Open>",
			job.ID, pbresources.BenchmarkJob_Status(job.Status).String(), teamName, job.TeamID, humanizeSince(now, job.CreatedAt), e.Cfg.PublicURLHost, job.ID))
	}

	if len(messages) == 0 {
		return nil
	}
	return e.Slack.Post(ctx, strings.Join(messages, "\n"))
}

// ClearContestData は競技データの全削除（リハーサル後のリセット、FR-S-13）。
func (e *Env) ClearContestData(ctx context.Context) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE teams SET best_benchmark_result_id = NULL, best_benchmark_result_for_audience_id = NULL, best_benchmark_result_for_admin_id = NULL`); err != nil {
		return err
	}
	for _, table := range []string{"benchmark_results", "benchmark_jobs", "survey_response_history", "survey_responses", "clarifications", "contestant_instances"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func humanizeSince(now, t time.Time) string {
	d := now.Sub(t).Round(time.Second)
	return d.String()
}
