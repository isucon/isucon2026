package model

import (
	"context"
	"database/sql"
)

// TeamRouter is deliberately separate from ContestantInstance: Router addresses
// must not be serialized into contestant/admin server lists or target-selection lists.
type TeamRouter struct{ PublicIP sql.NullString }

func FindTeamRouter(ctx context.Context, db DBTX, teamID int64) (*TeamRouter, error) {
	var router TeamRouter
	err := db.QueryRowContext(ctx, `SELECT public_ipv4_address FROM team_routers WHERE team_id=$1`, teamID).Scan(&router.PublicIP)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &router, err
}
func ExpectTeamRouter(ctx context.Context, db DBTX, teamID int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO team_routers(team_id) VALUES($1) ON CONFLICT(team_id) DO NOTHING`, teamID)
	return err
}
func RegisterTeamRouter(ctx context.Context, db DBTX, teamID int64, cloudID, privateIP, publicIP string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO team_routers(team_id,cloud_id,private_ipv4_address,public_ipv4_address) VALUES($1,$2,$3,$4)
 ON CONFLICT(team_id) DO UPDATE SET cloud_id=EXCLUDED.cloud_id,private_ipv4_address=EXCLUDED.private_ipv4_address,public_ipv4_address=EXCLUDED.public_ipv4_address,updated_at=now()`, teamID, cloudID, privateIP, publicIP)
	return err
}
