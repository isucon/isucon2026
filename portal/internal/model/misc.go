package model

import (
	"context"
	"database/sql"
	"strings"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

// --- Coupon ---

type Coupon struct {
	ID       int64
	TeamID   int64
	Code     string
	Activate bool
}

func FindCouponByTeam(ctx context.Context, db DBTX, teamID int64) (*Coupon, error) {
	c := &Coupon{}
	err := db.QueryRowContext(ctx, `SELECT id, team_id, code, activate FROM coupons WHERE team_id = $1 LIMIT 1`, teamID).
		Scan(&c.ID, &c.TeamID, &c.Code, &c.Activate)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Coupon) ToPB() *pbresources.Coupon {
	pb := &pbresources.Coupon{Id: c.ID, Activate: c.Activate}
	if c.Activate {
		pb.Code = strings.Split(c.Code, ",")
	}
	return pb
}

// --- PushSubscription ---

type PushSubscription struct {
	ID           int64
	ContestantID int64
	Endpoint     string
	P256dh       string
	Auth         string
}

func ContestantPushSubscriptions(ctx context.Context, db DBTX, contestantID int64) ([]*PushSubscription, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, contestant_id, endpoint, p256dh, auth FROM push_subscriptions WHERE contestant_id = $1`, contestantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PushSubscription
	for rows.Next() {
		s := &PushSubscription{}
		if err := rows.Scan(&s.ID, &s.ContestantID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- InstanceCommandExecuteRequest ---

type InstanceCommandExecuteRequest struct {
	ID        int64
	Command   sql.NullString
	CreatedAt time.Time
}

type InstanceCommandExecuteRequestResult struct {
	ID                   int64
	RequestID            int64
	ContestantInstanceID int64
	RouterTeamID         sql.NullInt64
	Output               sql.NullString
	ExitCode             sql.NullInt64
	FinishedAt           sql.NullTime
}

func FindCommandRequest(ctx context.Context, db DBTX, id int64) (*InstanceCommandExecuteRequest, error) {
	r := &InstanceCommandExecuteRequest{}
	err := db.QueryRowContext(ctx, `SELECT id, command, created_at FROM instance_command_execute_requests WHERE id = $1`, id).
		Scan(&r.ID, &r.Command, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func ListCommandRequests(ctx context.Context, db DBTX) ([]*InstanceCommandExecuteRequest, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, command, created_at FROM instance_command_execute_requests ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InstanceCommandExecuteRequest
	for rows.Next() {
		r := &InstanceCommandExecuteRequest{}
		if err := rows.Scan(&r.ID, &r.Command, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *InstanceCommandExecuteRequest) ToPB(ctx context.Context, db DBTX) (*pbresources.InstanceCommandExecuteRequest, error) {
	var total, finished int64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(finished_at)
		FROM instance_command_execute_request_results WHERE instance_command_execute_request_id = $1`, r.ID).
		Scan(&total, &finished)
	if err != nil {
		return nil, err
	}
	return &pbresources.InstanceCommandExecuteRequest{
		Id:                 r.ID,
		Command:            nullStr(r.Command),
		CreatedAt:          ts(r.CreatedAt),
		TotalTargetCount:   total,
		TotalFinishedCount: finished,
	}, nil
}

func FindCommandRequestResult(ctx context.Context, db DBTX, id int64) (*InstanceCommandExecuteRequestResult, error) {
	r := &InstanceCommandExecuteRequestResult{}
	err := db.QueryRowContext(ctx, `
		SELECT id, instance_command_execute_request_id, COALESCE(contestant_instance_id,0), router_team_id, output, exit_code, finished_at
		FROM instance_command_execute_request_results WHERE id = $1`, id).
		Scan(&r.ID, &r.RequestID, &r.ContestantInstanceID, &r.RouterTeamID, &r.Output, &r.ExitCode, &r.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func ListCommandRequestResults(ctx context.Context, db DBTX, requestID int64) ([]*InstanceCommandExecuteRequestResult, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, instance_command_execute_request_id, COALESCE(contestant_instance_id,0), router_team_id, output, exit_code, finished_at
		FROM instance_command_execute_request_results WHERE instance_command_execute_request_id = $1`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InstanceCommandExecuteRequestResult
	for rows.Next() {
		r := &InstanceCommandExecuteRequestResult{}
		if err := rows.Scan(&r.ID, &r.RequestID, &r.ContestantInstanceID, &r.RouterTeamID, &r.Output, &r.ExitCode, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *InstanceCommandExecuteRequestResult) ToPB(target *pbresources.ContestantInstance) *pbresources.InstanceCommandExecuteRequestResult {
	return &pbresources.InstanceCommandExecuteRequestResult{
		Id:         r.ID,
		Target:     target,
		ExitCode:   int32(orInt64(r.ExitCode, 0)),
		FinishedAt: tsn(r.FinishedAt),
	}
}
