package contest

import (
	"context"
	"database/sql"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/config"
)

// ContestPB は Contest.to_pb 相当。leaderboard 埋め込み用なので team なし版のみ。
func ContestPB(t config.Timing, now time.Time) *pbresources.Contest {
	utc := now.UTC()
	epoch := utc.Hour() / 8 * 8
	autoTime := func(addHours int) *timestamppb.Timestamp {
		return timestamppb.New(time.Date(utc.Year(), utc.Month(), utc.Day(), epoch+addHours, 0, 0, 0, time.UTC))
	}

	c := &pbresources.Contest{
		RegistrationOpensAt:  tsOrNil(t.RegistrationOpen),
		RegistrationClosesAt: tsOrNil(t.RegistrationClose),
		StartsAt:             autoTime(0),
		FreezesAt:            autoTime(7),
		EndsAt:               autoTime(8),
		Frozen:               ContestFrozen(t, now),
	}
	if t.ContestStart != nil {
		c.StartsAt = timestamppb.New(*t.ContestStart)
	}
	if t.ContestFreeze != nil {
		c.FreezesAt = timestamppb.New(*t.ContestFreeze)
	}
	if t.ContestEnd != nil {
		c.EndsAt = timestamppb.New(*t.ContestEnd)
	}

	switch {
	case ContestRunning(t, now):
		c.Status = pbresources.Contest_STARTED
	case ContestEnded(t, now):
		c.Status = pbresources.Contest_FINISHED
	case RegistrationOpen(t, now):
		c.Status = pbresources.Contest_REGISTRATION
	default:
		c.Status = pbresources.Contest_STANDBY
	}
	return c
}

// ContestPBForTeam は Contest.to_pb(team:) 相当。
// チームの追加時間で競技が開いていれば status を STARTED にする。
func ContestPBForTeam(ctx context.Context, db *sql.DB, t config.Timing, now time.Time, teamID int64) (*pbresources.Contest, error) {
	c := ContestPB(t, now)
	if teamID != 0 && c.Status != pbresources.Contest_STARTED {
		open, err := ContestOpenForTeam(ctx, db, t, teamID, now)
		if err != nil {
			return nil, err
		}
		if open {
			c.Status = pbresources.Contest_STARTED
		}
	}
	return c, nil
}

func tsOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
