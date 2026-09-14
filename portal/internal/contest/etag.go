package contest

import (
	"context"
	"database/sql"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
	"github.com/isucon/isucon2026/portal/internal/config"
)

// LeaderboardEtag は Contest.leaderboard_etag 相当。
// 合成 ETag の構成要素: チーム数・チーム最終更新・可視性内の最新結果（CT-5）。
func LeaderboardEtag(ctx context.Context, db *sql.DB, t config.Timing, admin bool, teamID int64) ([]byte, error) {
	var teamCount int64
	var teamLastUpdated sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(updated_at) FROM teams WHERE withdrawn = false AND disqualified = false`,
	).Scan(&teamCount, &teamLastUpdated); err != nil {
		return nil, err
	}

	b := &condBuilder{}
	b.add(successfullyFinished)
	if !admin {
		vis := VisibilityAudience
		if teamID != 0 {
			vis = VisibilityContestant
		}
		b.addVisibility(vis, t, teamID)
	}
	var latestID sql.NullInt64
	var latestAt sql.NullTime
	err := db.QueryRowContext(ctx,
		`SELECT id, marked_at FROM benchmark_results WHERE `+b.where()+` ORDER BY updated_at DESC LIMIT 1`,
		b.args...).Scan(&latestID, &latestAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	etag := &pbmisc.LeaderboardEtag{
		Admin:          admin,
		TeamId:         teamID,
		TeamCount:      teamCount,
		LatestResultId: latestID.Int64,
		HasProgress:    false,
	}
	if teamLastUpdated.Valid {
		etag.TeamLastUpdated = timestamppb.New(teamLastUpdated.Time)
	}
	if latestAt.Valid {
		etag.LatestResultAt = timestamppb.New(latestAt.Time)
	}
	return proto.Marshal(etag)
}
