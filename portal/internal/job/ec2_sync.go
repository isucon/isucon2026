package job

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"

	resources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/model"
)

var legacyBenchmarkHost = regexp.MustCompile(`^app\.team[1-9][0-9]*-aws\.127\.0\.0\.1\.nip\.io$`)

// SyncEC2Instances atomically registers tagged servers and internal Router endpoints.
func (e *Env) SyncEC2Instances(ctx context.Context, instances []external.EC2Instance) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[[2]int64]bool{}
	for _, i := range instances {
		key := [2]int64{i.TeamID, i.Number}
		if seen[key] {
			return fmt.Errorf("duplicate EC2 assignment: team %d number %d", i.TeamID, i.Number)
		}
		seen[key] = true
		team, err := model.FindTeam(ctx, tx, i.TeamID)
		if err != nil {
			return err
		}
		if team == nil {
			return fmt.Errorf("unknown portal team %d", i.TeamID)
		}
		status := resources.ContestantInstance_UNKNOWN
		switch i.State {
		case "running":
			status = resources.ContestantInstance_RUNNING
		case "stopped":
			status = resources.ContestantInstance_STOPPED
		case "pending":
			status = resources.ContestantInstance_PENDING
		case "stopping", "shutting-down":
			status = resources.ContestantInstance_MODIFYING
		default:
			return fmt.Errorf("unsupported EC2 state %q", i.State)
		}
		public := sql.NullString{String: i.PublicIPAddress, Valid: i.PublicIPAddress != "" && i.State == "running"}
		if i.Number == 5 {
			if !legacyBenchmarkHost.MatchString(i.BenchmarkHost) {
				return fmt.Errorf("invalid benchmark host for team %d", i.TeamID)
			}
			if err := model.SyncTeamRouter(ctx, tx, i.TeamID, i.InstanceID, i.PrivateIPAddress, public, sql.NullString{String: i.BenchmarkHost, Valid: true}); err != nil {
				return err
			}
		} else if i.Number >= 1 && i.Number <= 4 {
			if err := model.ExpectTeamRouter(ctx, tx, i.TeamID); err != nil {
				return err
			}
			if err := model.UpsertInstance(ctx, tx, i.TeamID, i.Number, i.InstanceID, status, i.PrivateIPAddress, public); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("invalid server number %d", i.Number)
		}
	}
	return tx.Commit()
}

// EnsureTrialTeams is an explicit operator action, never part of periodic discovery.
// Existing team IDs must already have the requested trial name; accounts are untouched.
func (e *Env) EnsureTrialTeams(ctx context.Context, ids []int64) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("invalid trial team ID %d", id)
		}
		name := fmt.Sprintf("trial-team%d", id)
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO teams(id,name,email_address,invite_token,student,is_hidden,created_at,updated_at) VALUES($1,$2,'',$3,false,true,now(),now()) ON CONFLICT(id) DO NOTHING`, id, name, hex.EncodeToString(token)); err != nil {
			return err
		}
		t, err := model.FindTeam(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.Name != name {
			return fmt.Errorf("team ID %d is already assigned to %q", id, t.Name)
		}
	}
	// Preserve the sequence even when explicitly assigned IDs exceed its current value.
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('teams','id'), GREATEST((SELECT last_value FROM teams_id_seq),(SELECT COALESCE(MAX(id),1) FROM teams)),true)`); err != nil {
		return err
	}
	return tx.Commit()
}
