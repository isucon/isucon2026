package registrationmigration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/isucon/isucon2026/portal/internal/model"
)

// Verify reads the imported records and checks identity, membership, consent and
// archived source fields against the snapshot. Errors never contain field values.
func (s Snapshot) Verify(ctx context.Context, db model.DBTX) error {
	if err := s.Validate(); err != nil {
		return err
	}
	expect := func(label, query string, args ...any) error {
		var matches bool
		if err := db.QueryRowContext(ctx, query, args...).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("import verification failed: %s", label)
		}
		return nil
	}
	archive := func(kind string, id int64, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return expect(kind+" archive", `SELECT EXISTS(SELECT 1 FROM registration_source_records WHERE model=$1 AND source_id=$2 AND payload=$3::jsonb)`, kind, id, string(encoded))
	}
	users := map[int64]User{}
	teams := map[int64]Team{}
	memberCount := 0
	for _, u := range s.Users {
		users[u.ID] = u
		if u.TeamID != nil {
			memberCount++
		}
	}
	for _, t := range s.Teams {
		teams[t.ID] = t
	}
	if err := expect("row counts", `SELECT (SELECT count(*) FROM teams)=$1 AND (SELECT count(*) FROM contestants)=$2 AND (SELECT count(*) FROM registration_records)=$2 AND (SELECT count(*) FROM registration_source_records)=$3`, len(s.Teams), memberCount, len(s.Teams)+len(s.Users)); err != nil {
		return err
	}
	for _, t := range s.Teams {
		if err := archive("authentication.Team", t.ID, t); err != nil {
			return err
		}
		var leader any
		if owner := users[t.Owner]; owner.TeamID != nil && *owner.TeamID == t.ID {
			leader = t.Owner
		}
		if err := expect("team fields", `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1 AND name=$2 AND leader_id IS NOT DISTINCT FROM $3 AND email_address=$4 AND is_hidden=$5 AND withdrawn=$6 AND disqualified=$7 AND availability_zone=$8 AND created_at=$9 AND updated_at=$10 AND final_participation=true)`, t.ID, t.Name, leader, users[t.Owner].Email, t.IsGuest, t.DeclinedAt != nil, t.BannedAt != nil, t.AWSAZ, t.CreatedAt, t.UpdatedAt); err != nil {
			return err
		}
	}
	for _, u := range s.Users {
		if err := archive("authentication.User", u.ID, u); err != nil {
			return err
		}
		if u.TeamID == nil {
			continue
		}
		if err := expect("contestant identity and membership", `SELECT EXISTS(SELECT 1 FROM contestants WHERE id=$1 AND team_id=$2 AND name=$3 AND github_id=$4 AND github_login=$5 AND discord_id=$6 AND discord_tag=$7 AND avatar_url=$8 AND student=$9 AND in_person=$10)`, u.ID, *u.TeamID, u.DisplayName, u.GitHubID, u.Username, u.DiscordID, u.DiscordUsername, s.Avatars[u.Icon], u.IsStudent, teams[*u.TeamID].IsLocalParticipation); err != nil {
			return err
		}
		if err := expect("registration consent", `SELECT EXISTS(SELECT 1 FROM registration_records WHERE contestant_id=$1 AND github_id=$2 AND email=$3 AND tshirt_size=$4 AND terms_agreed_at IS NOT DISTINCT FROM $5 AND privacy_agreed_at IS NOT DISTINCT FROM $6 AND sponsor_provide_agreed_at IS NOT DISTINCT FROM $7 AND sponsor_provide_withdrawn_at IS NOT DISTINCT FROM $8 AND minor_parental_consent IS NOT DISTINCT FROM $9)`, u.ID, u.GitHubID, u.Email, u.TShirtSize, u.TermsAgreedAt, u.PrivacyAgreedAt, u.SponsorAgreedAt, u.SponsorWithdrawnAt, u.MinorParentalConsent); err != nil {
			return err
		}
	}
	return nil
}
