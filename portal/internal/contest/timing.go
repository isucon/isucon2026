// Package contest は競技時刻の判定と leaderboard 生成を担う。
// 意味論は isucon14-portal の app/models/contest.rb に一致させる。
package contest

import (
	"context"
	"database/sql"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
)

// RegistrationOpen: open && close が両方設定されているときだけ範囲判定。
// それ以外は素通し（contest.rb registration_open? と同じ）。
func RegistrationOpen(t config.Timing, now time.Time) bool {
	if t.RegistrationOpen != nil && t.RegistrationClose != nil {
		return !now.Before(*t.RegistrationOpen) && now.Before(*t.RegistrationClose)
	}
	return true
}

// RegistrationInvitationClosed は招待参加の締切（FR-REG-4）。
func RegistrationInvitationClosed(t config.Timing, now time.Time) bool {
	return t.RegistrationInvitationClose != nil && !now.Before(*t.RegistrationInvitationClose)
}

// RegistrationUpdateClosed は登録内容の更新締切（FR-REG-5）。
func RegistrationUpdateClosed(t config.Timing, now time.Time) bool {
	return t.RegistrationUpdateClose != nil && !now.Before(*t.RegistrationUpdateClose)
}

func ContestRunning(t config.Timing, now time.Time) bool {
	if t.ContestStart != nil && t.ContestEnd != nil {
		return !now.Before(*t.ContestStart) && now.Before(*t.ContestEnd)
	}
	return true
}

func ContestStarted(t config.Timing, now time.Time) bool {
	return t.ContestStart != nil && !now.Before(*t.ContestStart)
}

func ContestFrozen(t config.Timing, now time.Time) bool {
	return t.ContestFreeze != nil && !now.Before(*t.ContestFreeze)
}

func ContestEnded(t config.Timing, now time.Time) bool {
	return t.ContestEnd != nil && !now.Before(*t.ContestEnd)
}

// ContestOpenForTeam はチーム別の追加時間（extra_time_assignments、final=false 固定 / DM-9）
// を考慮した競技時間判定。contest.rb contest_open_for_team? と同じ。
func ContestOpenForTeam(ctx context.Context, db *sql.DB, t config.Timing, teamID int64, now time.Time) (bool, error) {
	if !ContestEnded(t, now) && ContestRunning(t, now) {
		return true, nil
	}
	if teamID == 0 {
		return false, nil
	}
	if t.ContestStart == nil || t.ContestEnd == nil {
		return false, nil
	}

	var seconds int64
	err := db.QueryRowContext(ctx,
		`SELECT seconds FROM extra_time_assignments WHERE team_id = ? AND final = 0 LIMIT 1`, teamID,
	).Scan(&seconds)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	end := t.ContestEnd.Add(time.Duration(seconds) * time.Second)
	return !now.Before(*t.ContestStart) && now.Before(end), nil
}
