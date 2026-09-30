package model

import (
	"context"
	"database/sql"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type Contestant struct {
	ID                   int64
	TeamID               int64
	Name                 string
	GitHubLogin          string
	GitHubID             string
	DiscordID            string
	DiscordTag           string
	AvatarURL            string
	Student              bool
	IsDiscordGuildMember bool
	InPerson             sql.NullBool

	// isSSHKeyRegistered は to_pb(detail: true) 用に fillSSHKeyRegistered で埋める
	sshKeyRegistered bool
}

const contestantColumns = `contestants.id, contestants.team_id, contestants.name, contestants.github_login, contestants.github_id, contestants.discord_id, contestants.discord_tag, contestants.avatar_url, contestants.student, contestants.is_discord_guild_member, contestants.in_person`

func scanContestant(row interface{ Scan(...any) error }) (*Contestant, error) {
	c := &Contestant{}
	err := row.Scan(&c.ID, &c.TeamID, &c.Name, &c.GitHubLogin, &c.GitHubID, &c.DiscordID, &c.DiscordTag, &c.AvatarURL, &c.Student, &c.IsDiscordGuildMember, &c.InPerson)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func FindContestant(ctx context.Context, db DBTX, id int64) (*Contestant, error) {
	return scanContestant(db.QueryRowContext(ctx, `SELECT `+contestantColumns+` FROM contestants WHERE id = $1`, id))
}

// FindActiveContestant は Contestant.active（withdrawn でも disqualified でもないチーム所属）相当。
func FindActiveContestant(ctx context.Context, db DBTX, id int64) (*Contestant, error) {
	return scanContestant(db.QueryRowContext(ctx, `
		SELECT `+contestantColumns+` FROM contestants
		JOIN teams ON teams.id = contestants.team_id
		WHERE contestants.id = $1 AND teams.withdrawn = false AND teams.disqualified = false`, id))
}

// FindContestantIncludeDisqualified は Contestant.include_disqualified（withdrawn だけ除外）相当。
func FindContestantIncludeDisqualified(ctx context.Context, db DBTX, id int64) (*Contestant, error) {
	return scanContestant(db.QueryRowContext(ctx, `
		SELECT `+contestantColumns+` FROM contestants
		JOIN teams ON teams.id = contestants.team_id
		WHERE contestants.id = $1 AND teams.withdrawn = false`, id))
}

func FindContestantIncludeDisqualifiedBy(ctx context.Context, db DBTX, column, value string) (*Contestant, error) {
	if value == "" {
		return nil, nil
	}
	if column != "github_id" && column != "discord_id" {
		panic("unexpected column: " + column)
	}
	return scanContestant(db.QueryRowContext(ctx, `
		SELECT `+contestantColumns+` FROM contestants
		JOIN teams ON teams.id = contestants.team_id
		WHERE contestants.`+column+` = $1 AND teams.withdrawn = false`, value))
}

func TeamMembers(ctx context.Context, db DBTX, teamID int64) ([]*Contestant, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+contestantColumns+` FROM contestants WHERE team_id = $1 ORDER BY id ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Contestant
	for rows.Next() {
		c, err := scanContestant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fillSSHKeyRegistered は is_ssh_key_registered（to_pb detail 用）を一括で埋める。
func fillSSHKeyRegistered(ctx context.Context, db DBTX, members []*Contestant) error {
	if len(members) == 0 {
		return nil
	}
	byID := make(map[int64]*Contestant, len(members))
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		byID[m.ID] = m
		ids = append(ids, m.ID)
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT contestant_id FROM ssh_public_keys WHERE contestant_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if m, ok := byID[id]; ok {
			m.sshKeyRegistered = true
		}
	}
	return rows.Err()
}

func (c *Contestant) ToPB(detail bool) *pbresources.Contestant {
	pb := &pbresources.Contestant{
		Id:     c.ID,
		TeamId: c.TeamID,
		Name:   c.Name,
	}
	if detail {
		pb.Detail = &pbresources.Contestant_ContestantDetail{
			AvatarUrl:            c.AvatarURL,
			GithubLogin:          c.GitHubLogin,
			GithubId:             c.GitHubID,
			DiscordTag:           c.DiscordTag,
			DiscordId:            c.DiscordID,
			IsStudent:            c.Student,
			IsInPerson:           c.InPerson.Bool,
			IsSshKeyRegistered:   c.sshKeyRegistered,
			IsDiscordGuildMember: c.IsDiscordGuildMember,
		}
	}
	return pb
}

// ToPBWithSSHKeys は単体の Contestant で is_ssh_key_registered を埋めて to_pb(detail: true) する。
func (c *Contestant) ToPBWithSSHKeys(ctx context.Context, db DBTX) (*pbresources.Contestant, error) {
	if err := fillSSHKeyRegistered(ctx, db, []*Contestant{c}); err != nil {
		return nil, err
	}
	return c.ToPB(true), nil
}
