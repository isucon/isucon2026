package model

import (
	"context"
	"database/sql"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type Clarification struct {
	ID               int64
	TeamID           sql.NullInt64
	Disclosed        sql.NullBool
	Question         sql.NullString
	Answer           sql.NullString
	OriginalQuestion sql.NullString
	AnsweredAt       sql.NullTime
	Admin            bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const clarColumns = `clarifications.id, clarifications.team_id, clarifications.disclosed, clarifications.question, clarifications.answer, clarifications.original_question, clarifications.answered_at, clarifications.admin, clarifications.created_at, clarifications.updated_at`

func scanClar(row interface{ Scan(...any) error }) (*Clarification, error) {
	c := &Clarification{}
	err := row.Scan(&c.ID, &c.TeamID, &c.Disclosed, &c.Question, &c.Answer, &c.OriginalQuestion, &c.AnsweredAt, &c.Admin, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func FindClarification(ctx context.Context, db DBTX, id int64) (*Clarification, error) {
	return scanClar(db.QueryRowContext(ctx, `SELECT `+clarColumns+` FROM clarifications WHERE id = ?`, id))
}

func QueryClarifications(ctx context.Context, db DBTX, query string, args ...any) ([]*Clarification, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Clarification
	for rows.Next() {
		c, err := scanClar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (c *Clarification) Answered() bool    { return c.AnsweredAt.Valid }
func (c *Clarification) IsDisclosed() bool { return c.Disclosed.Valid && c.Disclosed.Bool }

func (c *Clarification) ToPB(team *pbresources.Team, originalQuestion bool) *pbresources.Clarification {
	pb := &pbresources.Clarification{
		Id:         c.ID,
		TeamId:     c.TeamID.Int64,
		Answered:   c.Answered(),
		Disclosed:  c.IsDisclosed(),
		Question:   nullStr(c.Question),
		Answer:     nullStr(c.Answer),
		CreatedAt:  ts(c.CreatedAt),
		AnsweredAt: tsn(c.AnsweredAt),
		Admin:      c.Admin,
		Team:       team,
	}
	if originalQuestion {
		pb.OriginalQuestion = nullStr(c.OriginalQuestion)
	}
	return pb
}

// ClarificationToPBWithTeam は to_pb(team: true, ...) 相当。
func ClarificationToPBWithTeam(ctx context.Context, db DBTX, c *Clarification, originalQuestion bool) (*pbresources.Clarification, error) {
	var teamPB *pbresources.Team
	if c.TeamID.Valid {
		team, err := FindTeam(ctx, db, c.TeamID.Int64)
		if err != nil {
			return nil, err
		}
		if team != nil {
			teamPB = team.ToPB(false, nil, false)
		}
	}
	return c.ToPB(teamPB, originalQuestion), nil
}
