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
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/isucon/isucon2026/portal/internal/model"
)

type Discord interface {
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

func (s *Service) Open(ctx context.Context, id int64) (thread string, err error) {
	err = s.locked(ctx, id, func(conn *sql.Conn) error {
		clar, e := model.FindClarification(ctx, conn, id)
		if e != nil {
			return e
		}
		if clar == nil || clar.Admin || !clar.TeamID.Valid {
			return errors.New("a participant clarification with a team is required")
		}
		var status string
		var idsJSON []byte
		var existing sql.NullString
		e = conn.QueryRowContext(ctx, `SELECT thread_id,status,participant_ids FROM discord_support_threads WHERE clarification_id=$1`, id).Scan(&existing, &status, &idsJSON)
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
		for _, uid := range ids {
			if e = s.Discord.CheckParticipant(ctx, uid); e != nil {
				return e
			}
		}

		if thread == "" {
			thread, e = s.Discord.EnsureThread(ctx, fmt.Sprintf("clar-%d", id))
			if e != nil {
				return e
			}
			if _, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET thread_id=$2,updated_at=now() WHERE clarification_id=$1`, id, thread); e != nil {
				return e
			}
		}
		for _, uid := range ids {
			if e = s.Discord.CheckParticipant(ctx, uid); e != nil {
				return e
			}
			if e = s.Discord.AddMember(ctx, thread, uid); e != nil {
				return e
			}
		}
		_, e = conn.ExecContext(ctx, `UPDATE discord_support_threads SET status='open',updated_at=now() WHERE clarification_id=$1`, id)
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
		// invited manually and people who have since left the team. Keep only the Bot.
		members, err := s.Discord.Members(ctx, thread)
		if err != nil {
			return err
		}
		for _, uid := range members {
			if err = s.Discord.RemoveMember(ctx, thread, uid); err != nil {
				return err
			}
		}
		remaining, err := s.Discord.Members(ctx, thread)
		if err != nil {
			return err
		}
		if len(remaining) != 0 {
			return errors.New("thread still has members; retry close")
		}
		if err = s.Discord.Archive(ctx, thread); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `UPDATE discord_support_threads SET status='closed',closed_at=now(),updated_at=now() WHERE clarification_id=$1`, id)
		return err
	})
}

// Poll opens only participant clarifications submitted after explicit activation.
// Failed opens and closes remain durable and are retried after bot restarts.
func (s *Service) Poll(ctx context.Context, since time.Time) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id FROM clarifications c LEFT JOIN discord_support_threads t ON t.clarification_id=c.id WHERE NOT c.admin AND c.team_id IS NOT NULL AND ((c.created_at >= $1 AND t.clarification_id IS NULL) OR t.status='opening') ORDER BY COALESCE(t.updated_at,c.created_at),c.id LIMIT 50`, since)
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
