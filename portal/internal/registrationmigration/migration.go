// Package registrationmigration imports a read-only Django registration snapshot.
package registrationmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/isucon/isucon2026/portal/internal/model"
)

type User struct {
	ID              int64  `json:"id"`
	TeamID          *int64 `json:"team_id"`
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	GitHubID        string `json:"github_id"`
	DiscordID       string `json:"discord_id"`
	DiscordUsername string `json:"discord_username"`
	Icon            string `json:"icon"`
	IsStudent       bool   `json:"is_student"`
	IsStaff         bool   `json:"is_staff"`
	IsActive        bool   `json:"is_active"`
	model.RegistrationRecord
}

type Team struct {
	ID                     int64      `json:"id"`
	Owner                  int64      `json:"owner_id"`
	Name                   string     `json:"name"`
	IsActive               bool       `json:"is_active"`
	IsGuest                bool       `json:"is_guest"`
	WantLocalParticipation bool       `json:"want_local_participation"`
	IsLocalParticipation   bool       `json:"is_local_participation"`
	AWSAZ                  string     `json:"aws_az"`
	DeclinedAt             *time.Time `json:"declined_at"`
	BannedAt               *time.Time `json:"banned_at"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	EnvcheckedAt           *time.Time `json:"envchecked_at"`
	CouponAgreedAt         *time.Time `json:"coupon_agreed_at"`
	AWSCoupon              string     `json:"aws_coupon"`
	SacloudCoupon          string     `json:"sacloud_coupon"`
}

// RegistrationCoupon is archived for audit; it does not activate cloud credits
// or enable legacy registration invitation redemption.
type RegistrationCoupon struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	TeamID    *int64     `json:"team_id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	UsedAt    *time.Time `json:"used_at"`
}

// Other lists every non-registration application model, including zero counts.
// Nonempty unsupported models block import instead of silently losing data.
var requiredInventory = []string{"authentication.RegisterCoupon", "contact.Ticket", "contact.TicketComment", "contest.Information", "contest.Server", "contest.Job", "contest.Score", "envcheck.EnvCheckResult"}

// EmptyInventory names every legacy application model that this registration-only importer does not convert.
func EmptyInventory() map[string]int {
	out := make(map[string]int, len(requiredInventory))
	for _, name := range requiredInventory {
		out[name] = 0
	}
	return out
}

type Snapshot struct {
	RegistrationCoupons []RegistrationCoupon `json:"registration_coupons,omitempty"`
	Version             int                  `json:"version"`
	Users               []User               `json:"users"`
	Teams               []Team               `json:"teams"`
	Other               map[string]int       `json:"other"`
	Avatars             map[string]string    `json:"avatars"`
}

func Decode(r io.Reader) (*Snapshot, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var s Snapshot
	if err := decoder.Decode(&s); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("snapshot must contain one JSON object")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s Snapshot) Validate() error {
	if s.Version != 1 {
		return fmt.Errorf("unsupported snapshot version %d", s.Version)
	}
	if s.Other == nil {
		return fmt.Errorf("model inventory is required")
	}
	for _, name := range requiredInventory {
		if _, ok := s.Other[name]; !ok {
			return fmt.Errorf("missing model inventory: %s", name)
		}
	}
	for name, count := range s.Other {
		if name == "authentication.RegisterCoupon" {
			if count != len(s.RegistrationCoupons) {
				return fmt.Errorf("registration coupon inventory mismatch")
			}
			continue
		}
		if count != 0 {
			return fmt.Errorf("unsupported model %s contains %d records", name, count)
		}
	}
	users := map[int64]User{}
	teams := map[int64]Team{}
	github, discord := map[string]bool{}, map[string]bool{}
	names := map[string]bool{}
	for _, team := range s.Teams {
		if team.ID <= 0 || teams[team.ID].ID != 0 || team.Name == "" || names[team.Name] {
			return fmt.Errorf("invalid or duplicate team %d", team.ID)
		}
		if !team.IsActive && team.DeclinedAt == nil && team.BannedAt == nil {
			return fmt.Errorf("team %d inactive without decline or ban", team.ID)
		}
		if strings.Contains(team.AWSCoupon, ",") || strings.Contains(team.SacloudCoupon, ",") {
			return fmt.Errorf("team %d coupon contains the target delimiter", team.ID)
		}
		if team.CreatedAt.IsZero() || team.UpdatedAt.IsZero() {
			return fmt.Errorf("team %d is missing timestamps", team.ID)
		}
		if team.AWSAZ == "" {
			return fmt.Errorf("team %d has no availability zone", team.ID)
		}
		teams[team.ID] = team
		names[team.Name] = true
	}
	couponIDs, couponTokens := map[int64]bool{}, map[string]bool{}
	for _, coupon := range s.RegistrationCoupons {
		if coupon.ID <= 0 || couponIDs[coupon.ID] || coupon.Token == "" || couponTokens[coupon.Token] || coupon.CreatedAt.IsZero() || coupon.UpdatedAt.IsZero() {
			return fmt.Errorf("invalid or duplicate registration coupon")
		}
		if coupon.TeamID != nil && teams[*coupon.TeamID].ID == 0 {
			return fmt.Errorf("registration coupon has unknown team")
		}
		couponIDs[coupon.ID], couponTokens[coupon.Token] = true, true
	}
	counts := map[int64]int{}
	for _, user := range s.Users {
		if user.ID <= 0 || users[user.ID].ID != 0 {
			return fmt.Errorf("invalid or duplicate user %d", user.ID)
		}
		users[user.ID] = user
		if user.TeamID == nil {
			continue
		} // Archived only, never converted into staff accounts.
		if teams[*user.TeamID].ID == 0 {
			return fmt.Errorf("user %d has unknown team", user.ID)
		}
		if !user.IsActive {
			return fmt.Errorf("user %d is disabled in Django; no equivalent contestant state", user.ID)
		}
		counts[*user.TeamID]++
		if counts[*user.TeamID] > 3 {
			return fmt.Errorf("team %d has more than three members", *user.TeamID)
		}
		id, err := strconv.ParseUint(user.GitHubID, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != user.GitHubID || github[user.GitHubID] {
			return fmt.Errorf("user %d has missing, invalid or duplicate GitHub ID", user.ID)
		}
		github[user.GitHubID] = true
		if user.DiscordID != "" {
			if discord[user.DiscordID] {
				return fmt.Errorf("user %d has duplicate Discord ID", user.ID)
			}
			discord[user.DiscordID] = true
		}
		if user.Username == "" || user.DisplayName == "" {
			return fmt.Errorf("user %d has missing name", user.ID)
		}
		if user.Icon != "" {
			u, err := url.Parse(s.Avatars[user.Icon])
			if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
				return fmt.Errorf("user %d needs a permanent HTTPS avatar URL", user.ID)
			}
		}
	}
	for _, team := range s.Teams {
		owner, ok := users[team.Owner]
		if !ok {
			return fmt.Errorf("team %d has unknown owner", team.ID)
		}
		// Django decline clears membership. Preserve its historical owner in source records.
		if team.DeclinedAt == nil && (owner.TeamID == nil || *owner.TeamID != team.ID) {
			return fmt.Errorf("team %d owner is not a member", team.ID)
		}
	}
	return nil
}

// Import is all-or-nothing and refuses populated destinations. The caller applies DB migrations first.
func (s Snapshot) Import(ctx context.Context, db *sql.DB) error {
	if err := s.Validate(); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Prevent concurrent registration or a second importer between emptiness checks and inserts.
	tables := []string{"teams", "contestants", "registration_records", "registration_source_records", "coupons", "env_checks", "benchmark_executions", "benchmark_jobs", "benchmark_results", "clarifications", "contestant_instances", "extra_time_assignments", "notifications", "push_subscriptions", "ssh_public_keys", "survey_response_history", "survey_responses", "instance_command_execute_requests", "instance_command_execute_request_results", "jobs"}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE `+strings.Join(tables, ",")+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		return err
	}
	var occupied bool
	queries := make([]string, len(tables))
	for i, table := range tables {
		queries[i] = "SELECT 1 FROM " + table
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(`+strings.Join(queries, " UNION ALL ")+`)`).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return fmt.Errorf("destination registration tables must be empty")
	}
	users := map[int64]User{}
	teams := map[int64]Team{}
	for _, user := range s.Users {
		users[user.ID] = user
	}
	archive := func(kind string, id int64, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO registration_source_records(model,source_id,payload) VALUES($1,$2,$3)`, kind, id, payload)
		return err
	}
	for _, coupon := range s.RegistrationCoupons {
		if err := archive("authentication.RegisterCoupon", coupon.ID, coupon); err != nil {
			return err
		}
	}
	for _, team := range s.Teams {
		teams[team.ID] = team
		if err := archive("authentication.Team", team.ID, team); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO teams(id,name,email_address,invite_token,is_hidden,final_participation,student,availability_zone,withdrawn,disqualified,created_at,updated_at) VALUES($1,$2,$3,$4,$5,true,false,$6,$7,$8,$9,$10)`, team.ID, team.Name, users[team.Owner].Email, model.NewInviteToken(), team.IsGuest, team.AWSAZ, team.DeclinedAt != nil, team.BannedAt != nil, team.CreatedAt, team.UpdatedAt)
		if err != nil {
			return fmt.Errorf("import team %d: %w", team.ID, err)
		}
		codes := []string{}
		if team.AWSCoupon != "" {
			codes = append(codes, team.AWSCoupon)
		}
		if team.SacloudCoupon != "" {
			codes = append(codes, team.SacloudCoupon)
		}
		if len(codes) > 0 {
			code := strings.Join(codes, ",")
			if _, err := tx.ExecContext(ctx, `INSERT INTO coupons(team_id,code,activate,created_at,updated_at) VALUES($1,$2,$3,$4,$5)`, team.ID, code, team.CouponAgreedAt != nil, team.CreatedAt, team.UpdatedAt); err != nil {
				return err
			}
		}
	}
	for _, user := range s.Users {
		if err := archive("authentication.User", user.ID, user); err != nil {
			return err
		}
		if user.TeamID == nil {
			continue
		}
		team := teams[*user.TeamID]
		_, err := tx.ExecContext(ctx, `INSERT INTO contestants(id,team_id,name,github_login,github_id,discord_id,discord_tag,avatar_url,student,in_person,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, user.ID, *user.TeamID, user.DisplayName, user.Username, user.GitHubID, user.DiscordID, user.DiscordUsername, s.Avatars[user.Icon], user.IsStudent, team.IsLocalParticipation, team.CreatedAt, team.UpdatedAt)
		if err != nil {
			return fmt.Errorf("import user %d: %w", user.ID, err)
		}
		if err := model.SaveRegistrationRecord(ctx, tx, user.ID, user.RegistrationRecord); err != nil {
			return err
		}
	}
	for _, team := range s.Teams {
		owner := users[team.Owner]
		if owner.TeamID != nil && *owner.TeamID == team.ID {
			if _, err := tx.ExecContext(ctx, `UPDATE teams SET leader_id=$1 WHERE id=$2`, team.Owner, team.ID); err != nil {
				return err
			}
		}
		// Derive student status without changing the source timestamp.
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET student=(SELECT COUNT(*)>0 AND COUNT(*)=COUNT(*) FILTER (WHERE student) FROM contestants WHERE team_id=teams.id) WHERE id=$1`, team.ID); err != nil {
			return err
		}
	}
	// ALTER SEQUENCE is transactional, unlike setval. Failed imports leave sequence state intact.
	for _, table := range []string{"teams", "contestants"} {
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0)+1 FROM `+table).Scan(&next); err != nil {
			return err
		}
		if table == "contestants" {
			for _, user := range s.Users {
				if user.ID >= next {
					next = user.ID + 1
				}
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER SEQUENCE %s_id_seq RESTART WITH %d", table, next)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
