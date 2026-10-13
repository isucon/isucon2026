package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"slices"
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
		`SELECT `+envCheckColumns+` FROM env_checks WHERE team_id = $1 AND name = $2 ORDER BY id DESC LIMIT 1`, teamID, name))
}

func TeamEnvChecks(ctx context.Context, db DBTX, teamID int64) ([]*EnvCheck, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+envCheckColumns+` FROM env_checks WHERE team_id = $1 ORDER BY id DESC`, teamID)
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
	return db.QueryRowContext(ctx, `
		INSERT INTO env_checks (team_id, name, ip_address, passed, message, admin_message, raw_data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING id`,
		e.TeamID, e.Name, e.IPAddress, e.Passed, e.Message, e.AdminMessage, e.RawData).Scan(&e.ID)
}

// EnvCheckProgress contains the state of the latest five-instance environment.
// Reports identify that environment by its EC2 instance IDs, not reusable EIPs.
type EnvCheckProgress struct {
	Status      pbresources.EnvCheckStatus
	InstanceIPs []string
}

func EnvCheckStatus(ctx context.Context, db DBTX, teamID int64) (pbresources.EnvCheckStatus, error) {
	progress, err := GetEnvCheckProgress(ctx, db, teamID)
	return progress.Status, err
}

func GetEnvCheckProgress(ctx context.Context, db DBTX, teamID int64) (EnvCheckProgress, error) {
	if teamID == 0 {
		return EnvCheckProgress{Status: pbresources.EnvCheckStatus_PREPARING}, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT `+envCheckColumns+` FROM env_checks WHERE team_id = $1 AND name IN ('test-boot', 'test-ssh') ORDER BY id DESC`, teamID)
	if err != nil {
		return EnvCheckProgress{}, err
	}
	defer rows.Close()
	var checks []*EnvCheck
	for rows.Next() {
		check, err := scanEnvCheck(rows)
		if err != nil {
			return EnvCheckProgress{}, err
		}
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return EnvCheckProgress{}, err
	}
	return envCheckProgress(checks), nil
}

type envCheckSnapshot struct {
	InstanceID string `json:"instance_id"`
	Instances  []struct {
		ID string `json:"instance_id"`
	} `json:"instances"`
}

func envCheckIdentity(check *EnvCheck) (string, []string) {
	var snapshot envCheckSnapshot
	if json.Unmarshal([]byte(check.RawData), &snapshot) != nil || len(snapshot.Instances) != 5 {
		return "", nil
	}
	ids := make([]string, 0, 5)
	for _, instance := range snapshot.Instances {
		if instance.ID == "" || slices.Contains(ids, instance.ID) {
			return "", nil
		}
		ids = append(ids, instance.ID)
	}
	if !slices.Contains(ids, snapshot.InstanceID) {
		return "", nil
	}
	slices.Sort(ids)
	return snapshot.InstanceID, ids
}

// checks must be ordered by descending ID, so retries never hide newer failures.
func envCheckProgress(checks []*EnvCheck) EnvCheckProgress {
	progress := EnvCheckProgress{Status: pbresources.EnvCheckStatus_NOT_STARTED}
	if len(checks) == 0 {
		return progress
	}
	var ids []string
	for _, check := range checks {
		if check.Name == "test-boot" {
			_, ids = envCheckIdentity(check)
			break
		}
	}
	if len(ids) != 5 {
		return progress
	}
	boot, ssh := map[string]*EnvCheck{}, map[string]*EnvCheck{}
	for _, check := range checks {
		id, environment := envCheckIdentity(check)
		if !slices.Equal(ids, environment) {
			continue
		}
		switch check.Name {
		case "test-boot":
			if boot[id] == nil {
				boot[id] = check
			}
		case "test-ssh":
			if ssh[id] == nil {
				ssh[id] = check
			}
		}
	}
	allBoot, allSSH := true, true
	for _, id := range ids {
		b, s := boot[id], ssh[id]
		if b == nil || !b.Passed || net.ParseIP(b.IPAddress) == nil {
			allBoot = false
			continue
		}
		if slices.Contains(progress.InstanceIPs, b.IPAddress) {
			allBoot = false
		} else {
			progress.InstanceIPs = append(progress.InstanceIPs, b.IPAddress)
		}
		if s == nil || !s.Passed || s.ID < b.ID || s.IPAddress != b.IPAddress {
			allSSH = false
		}
	}
	if allBoot {
		progress.Status = pbresources.EnvCheckStatus_CREATED_INSTANCE
		if allSSH {
			progress.Status = pbresources.EnvCheckStatus_DONE
		}
	}
	return progress
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
