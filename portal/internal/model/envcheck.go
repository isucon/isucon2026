package model

import (
	"context"
	"database/sql"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type EnvCheck struct {
	ID           int64
	TeamID       int64
	Name         string
	IPAddress    string
	Passed       bool
	Message      string
	AdminMessage string
	RawData      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const envCheckColumns = `env_checks.id, env_checks.team_id, env_checks.name, env_checks.ip_address, env_checks.passed, env_checks.message, env_checks.admin_message, env_checks.raw_data, env_checks.created_at, env_checks.updated_at`

func scanEnvCheck(row interface{ Scan(...any) error }) (*EnvCheck, error) {
	e := &EnvCheck{}
	err := row.Scan(&e.ID, &e.TeamID, &e.Name, &e.IPAddress, &e.Passed, &e.Message, &e.AdminMessage, &e.RawData, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func LastEnvCheck(ctx context.Context, db DBTX, teamID int64, name string) (*EnvCheck, error) {
	return scanEnvCheck(db.QueryRowContext(ctx,
		`SELECT `+envCheckColumns+` FROM env_checks WHERE team_id = ? AND name = ? ORDER BY id DESC LIMIT 1`, teamID, name))
}

func TeamEnvChecks(ctx context.Context, db DBTX, teamID int64) ([]*EnvCheck, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+envCheckColumns+` FROM env_checks WHERE team_id = ? ORDER BY id DESC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EnvCheck
	for rows.Next() {
		e, err := scanEnvCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func CreateEnvCheck(ctx context.Context, db DBTX, e *EnvCheck) error {
	res, err := db.ExecContext(ctx, `
		INSERT INTO env_checks (team_id, name, ip_address, passed, message, admin_message, raw_data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		e.TeamID, e.Name, e.IPAddress, e.Passed, e.Message, e.AdminMessage, e.RawData)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// EnvCheckStatus は EnvCheck.status 相当。
// Rails は感想戦対応でステータスを DONE に固定している（FIXME を踏襲）。
func EnvCheckStatus(ctx context.Context, db DBTX, teamID int64) (pbresources.EnvCheckStatus, error) {
	return pbresources.EnvCheckStatus_DONE, nil
}

func (e *EnvCheck) ToPB() *pbresources.EnvCheck {
	return &pbresources.EnvCheck{
		Id:           e.ID,
		TeamId:       e.TeamID,
		Name:         e.Name,
		IpAddress:    e.IPAddress,
		Passed:       e.Passed,
		Message:      e.Message,
		AdminMessage: e.AdminMessage,
		RawData:      e.RawData,
		CreatedAt:    ts(e.CreatedAt),
		UpdatedAt:    ts(e.UpdatedAt),
	}
}
