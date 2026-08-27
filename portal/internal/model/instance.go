package model

import (
	"context"
	"database/sql"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type ContestantInstance struct {
	ID                 int64
	TeamID             int64
	CloudID            string
	Number             int64
	Status             int32 // DM-2: proto の enum 値と共通
	PrivateIPv4Address string
	PublicIPv4Address  sql.NullString
}

const instanceColumns = `contestant_instances.id, contestant_instances.team_id, contestant_instances.cloud_id, contestant_instances.number, contestant_instances.status, contestant_instances.private_ipv4_address, contestant_instances.public_ipv4_address`

func scanInstance(row interface{ Scan(...any) error }) (*ContestantInstance, error) {
	ci := &ContestantInstance{}
	err := row.Scan(&ci.ID, &ci.TeamID, &ci.CloudID, &ci.Number, &ci.Status, &ci.PrivateIPv4Address, &ci.PublicIPv4Address)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ci, nil
}

func FindInstance(ctx context.Context, db DBTX, id int64) (*ContestantInstance, error) {
	return scanInstance(db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM contestant_instances WHERE id = ?`, id))
}

func FindInstanceByTeamAndID(ctx context.Context, db DBTX, teamID, id int64) (*ContestantInstance, error) {
	return scanInstance(db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM contestant_instances WHERE team_id = ? AND id = ?`, teamID, id))
}

func FindInstanceByTeamAndNumber(ctx context.Context, db DBTX, teamID, number int64) (*ContestantInstance, error) {
	return scanInstance(db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM contestant_instances WHERE team_id = ? AND number = ?`, teamID, number))
}

func TeamInstances(ctx context.Context, db DBTX, teamID int64) ([]*ContestantInstance, error) {
	return queryInstances(ctx, db, `SELECT `+instanceColumns+` FROM contestant_instances WHERE team_id = ? ORDER BY number ASC`, teamID)
}

func AllInstancesOrderByCloudID(ctx context.Context, db DBTX) ([]*ContestantInstance, error) {
	return queryInstances(ctx, db, `SELECT `+instanceColumns+` FROM contestant_instances ORDER BY cloud_id ASC`)
}

func queryInstances(ctx context.Context, db DBTX, query string, args ...any) ([]*ContestantInstance, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ContestantInstance
	for rows.Next() {
		ci, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

// UpsertInstance は find_or_initialize_by(team_id, number) → update! 相当。
func UpsertInstance(ctx context.Context, db DBTX, teamID, number int64, cloudID string, status pbresources.ContestantInstance_Status, privateIP string, publicIP sql.NullString) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO contestant_instances (team_id, number, cloud_id, status, private_ipv4_address, public_ipv4_address, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE cloud_id = VALUES(cloud_id), status = VALUES(status),
			private_ipv4_address = VALUES(private_ipv4_address), public_ipv4_address = VALUES(public_ipv4_address),
			updated_at = UTC_TIMESTAMP(6)`,
		teamID, number, cloudID, int32(status), privateIP, publicIP)
	return err
}

func (ci *ContestantInstance) ToPB(team *pbresources.Team) *pbresources.ContestantInstance {
	return &pbresources.ContestantInstance{
		Id:                 ci.ID,
		CloudId:            ci.CloudID,
		TeamId:             ci.TeamID,
		Number:             ci.Number,
		Status:             pbresources.ContestantInstance_Status(ci.Status),
		PublicIpv4Address:  nullStr(ci.PublicIPv4Address),
		PrivateIpv4Address: ci.PrivateIPv4Address,
		Team:               team,
	}
}
