// Package supportthread manages one private Discord conversation per clarification.
// Transcripts are stored separately from participant-visible clarification answers.
package supportthread

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/isucon/isucon2026/portal/internal/model"
)

type Discord interface {
	StaffIDs(context.Context) ([]string, error)
	EnsureThread(context.Context, string) (string, error)
	CheckParticipant(context.Context, string) error
	AddMember(context.Context, string, string) error
	Lock(context.Context, string) error
	Messages(context.Context, string) ([]*discordgo.Message, error)
	Members(context.Context, string) ([]string, error)
	RemoveMember(context.Context, string, string) error
	Archive(context.Context, string) error
}

type Service struct {
	DB      *sql.DB
	Discord Discord
}

// locked serializes retries and open/close across bot processes. The lock and all
// database writes use the same connection, and survive neither a crash nor restart.
func (s *Service) locked(ctx context.Context, id int64, fn func(*sql.Conn) error) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(15430027, $1::integer)`, id); err != nil {
		return err
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(unlock, `SELECT pg_advisory_unlock(15430027, $1::integer)`, id); e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return fn(conn)
}

// OpenTeam starts an operator-requested conversation. A team can have one active
// conversation; a later command after close creates a new log without reusing it.
func (s *Service) OpenTeam(ctx context.Context, teamID int64) (string, error) {
	if teamID <= 0 {
		return "", errors.New("invalid team ID")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15430028,$1::integer)`, teamID); err != nil {
		return "", err
	}
	team, err := model.FindTeam(ctx, tx, teamID)
	if err != nil {
		return "", err
	}
	if team == nil {
		return "", errors.New("team not found")
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT t.clarification_id FROM discord_support_threads t JOIN clarifications c ON c.id=t.clarification_id WHERE c.team_id=$1 AND t.status<>'closed' ORDER BY t.clarification_id LIMIT 1`, teamID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.QueryRowContext(ctx, `INSERT INTO clarifications(team_id,disclosed,original_question,question,admin,created_at,updated_at) VALUES($1,false,'Discord Support Thread','Discord Support Thread',true,now(),now()) RETURNING id`, teamID).Scan(&id); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO discord_support_threads(clarification_id,participant_ids,status) VALUES($1,'[]','opening')`, id); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return s.Open(ctx, id)
}

// CloseTeam ends the team's active conversation from the operator-only channel.
func (s *Service) CloseTeam(ctx context.Context, teamID int64) error {
	var thread sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT t.thread_id FROM discord_support_threads t JOIN clarifications c ON c.id=t.clarification_id WHERE c.team_id=$1 AND t.status<>'closed' ORDER BY t.clarification_id LIMIT 1`, teamID).Scan(&thread)
	if err != nil {
		return err
	}
	if !thread.Valid {
		return errors.New("support thread is still opening")
	}
	return s.Close(ctx, thread.String)
}

func (s *Service) Open(ctx context.Context, id int64) (thread string, err error) {
	err = s.locked(ctx, id, func(conn *sql.Conn) error {
		clar, e := model.FindClarification(ctx, conn, id)
		if e != nil {
			return e
		}
		if clar == nil || !clar.TeamID.Valid {
			return errors.New("a participant clarification with a team is required")
		}
		var status string
		var idsJSON []byte
		var existing sql.NullString
		e = conn.QueryRowContext(ctx, `SELECT thread_id,status,participant_ids FROM discord_support_threads WHERE clarification_id=$1`, id).Scan(&existing, &status, &idsJSON)
		if clar.Admin && errors.Is(e, sql.ErrNoRows) {
			return errors.New("announcement is not a support conversation")
		}
		if errors.Is(e, sql.ErrNoRows) {
			if _, e = conn.ExecContext(ctx, `INSERT INTO discord_support_threads(clarification_id,participant_ids,status) VALUES($1,'[]','opening')`, id); e != nil {
				return e
			}
			status = "opening"
			idsJSON = []byte("[]")
		} else if e != nil {
			return e
		}
		thread = existing.String
		if status == "open" {
			_, e = s.inviteStaff(ctx, thread)
			return e
		}
		if status != "opening" {
			return nil
		}
		if _, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET updated_at=now() WHERE clarification_id=$1`, id); e != nil {
			return e
		}
		var ids []string
		if e = json.Unmarshal(idsJSON, &ids); e != nil {
			return e
		}
		if len(ids) == 0 {
			members, e := model.TeamMembers(ctx, conn, clar.TeamID.Int64)
			if e != nil {
				return e
			}
			seen := map[string]bool{}
			for _, m := range members {
				if m.DiscordID != "" && !seen[m.DiscordID] {
					ids = append(ids, m.DiscordID)
					seen[m.DiscordID] = true
				}
			}
			if len(ids) == 0 {
				return errors.New("team has no linked Discord members")
			}
			raw, e := json.Marshal(ids)
			if e != nil {
				return e
			}
			if _, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET participant_ids=$2 WHERE clarification_id=$1`, id, string(raw)); e != nil {
				return e
			}
		}
		eligible := make([]string, 0, len(ids))
		for _, uid := range ids {
			if e = s.Discord.CheckParticipant(ctx, uid); isUnknownMember(e) {
				continue
			} else if e != nil {
				return e
			}
			eligible = append(eligible, uid)
		}
		if len(eligible) == 0 {
			return errors.New("team has no Discord members currently in the server")
		}

		if thread == "" {
			thread, e = s.Discord.EnsureThread(ctx, fmt.Sprintf("support-%d", id))
			if e != nil {
				return e
			}
			if _, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET thread_id=$2,updated_at=now() WHERE clarification_id=$1`, id, thread); e != nil {
				return e
			}
		}
		invited := make([]string, 0, len(eligible))
		for _, uid := range eligible {
			// A participant can leave between preflight and invitation.
			if e = s.Discord.CheckParticipant(ctx, uid); isUnknownMember(e) {
				continue
			} else if e != nil {
				return e
			}
			if e = s.Discord.AddMember(ctx, thread, uid); isUnknownMember(e) {
				continue
			} else if e != nil {
				return e
			}
			invited = append(invited, uid)
		}
		if len(invited) == 0 {
			return errors.New("team has no Discord members currently in the server")
		}
		if _, e = s.inviteStaff(ctx, thread); e != nil {
			return e
		}
		raw, e := json.Marshal(invited)
		if e != nil {
			return e
		}
		// Keep the original candidates until opening succeeds so retries can
		// include members who join while the conversation is still pending.
		_, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET status='open',participant_ids=$2,updated_at=now() WHERE clarification_id=$1`, id, string(raw))
		return e
	})
	return
}

func (s *Service) Close(ctx context.Context, thread string) error {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT clarification_id FROM discord_support_threads WHERE thread_id=$1`, thread).Scan(&id); err != nil {
		return err
	}
	return s.locked(ctx, id, func(conn *sql.Conn) error {
		var status string
		if err := conn.QueryRowContext(ctx, `SELECT status FROM discord_support_threads WHERE clarification_id=$1`, id).Scan(&status); err != nil {
			return err
		}
		if status == "closed" {
			return nil
		}
		if _, err := conn.ExecContext(ctx, `UPDATE discord_support_threads SET status='closing',updated_at=now() WHERE clarification_id=$1`, id); err != nil {
			return err
		}
		if err := s.Discord.Lock(ctx, thread); err != nil {
			return err
		}
		var saved bool
		if err := conn.QueryRowContext(ctx, `SELECT transcript_saved_at IS NOT NULL FROM discord_support_threads WHERE clarification_id=$1`, id).Scan(&saved); err != nil {
			return err
		}
		if !saved {
			messages, err := s.Discord.Messages(ctx, thread)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(messages)
			if err != nil {
				return err
			}
			if _, err = conn.ExecContext(ctx, `UPDATE discord_support_threads SET transcript=$2,transcript_saved_at=now(),updated_at=now() WHERE clarification_id=$1`, id, string(raw)); err != nil {
				return err
			}
		}

		// Enumerate actual membership, not current team membership: also remove guests
		// invited manually and people who have since left the team. Retain staff and the Bot.
		staff, err := s.inviteStaff(ctx, thread)
		if err != nil {
			return err
		}
		members, err := s.Discord.Members(ctx, thread)
		if err != nil {
			return err
		}
		for _, uid := range members {
			if staff[uid] {
				continue
			}
			if err = s.Discord.RemoveMember(ctx, thread, uid); err != nil {
				return err
			}
		}
		remaining, err := s.Discord.Members(ctx, thread)
		if err != nil {
			return err
		}
		for _, uid := range remaining {
			if !staff[uid] {
				return errors.New("thread still has non-staff members; retry close")
			}
		}
		if err = s.Discord.Archive(ctx, thread); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `UPDATE discord_support_threads SET status='closed',closed_at=now(),updated_at=now() WHERE clarification_id=$1`, id)
		return err
	})
}

// RetryPending resumes only operations explicitly initiated by an operator.
// It never discovers or opens new clarifications.
func (s *Service) RetryPending(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT clarification_id FROM discord_support_threads WHERE status='opening' ORDER BY updated_at,clarification_id LIMIT 50`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var errs []error
	rows, err = s.DB.QueryContext(ctx, `SELECT thread_id FROM discord_support_threads WHERE status='closing' AND thread_id IS NOT NULL`)
	if err != nil {
		return err
	}
	var threads []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		threads = append(threads, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range threads {
		if err = s.Close(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("close thread %s: %w", id, err))
		}
	}
	for _, id := range ids {
		if _, err = s.Open(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("open clar %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) inviteStaff(ctx context.Context, thread string) (map[string]bool, error) {
	ids, err := s.Discord.StaffIDs(ctx)
	if err != nil {
		return nil, err
	}
	staff := make(map[string]bool, len(ids))
	for _, id := range ids {
		if staff[id] {
			continue
		}
		if err := s.Discord.AddMember(ctx, thread, id); err != nil {
			return nil, err
		}
		staff[id] = true
	}
	return staff, nil
}

// Only Discord's explicit Unknown Member response means a participant is absent.
// Permission failures, missing channels, and transport errors must remain visible.
func isUnknownMember(err error) bool {
	var rest *discordgo.RESTError
	return errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusNotFound && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMember
}
