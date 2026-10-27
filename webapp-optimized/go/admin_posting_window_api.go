package isureal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type openPostingWindowRequest struct {
	Region        string `json:"region"`
	LocalDate     string `json:"localDate"`
	OnTimeSeconds *int64 `json:"onTimeSeconds"`
}

type openPostingWindowResponse struct {
	ID             string    `json:"id"`
	Region         string    `json:"region"`
	LocalDate      string    `json:"localDate"`
	StartsAt       time.Time `json:"startsAt"`
	OnTimeDeadline time.Time `json:"onTimeDeadline"`
}

// notificationWriter writes the notification rows of opened windows in the
// background. The notifications are delivered from the hot state as soon as
// the window opens; the rows (thousands per region, into cold indexes) are
// the durable record and need not delay the opening. Initialization waits
// for pending writes so a reset cannot interleave with them.
type notificationWriter struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

var notificationWrites = newNotificationWriter()

func newNotificationWriter() *notificationWriter {
	ctx, cancel := context.WithCancel(context.Background())
	return &notificationWriter{ctx: ctx, cancel: cancel}
}

func (w *notificationWriter) write(db *sqlx.DB, windowID string, startsAt time.Time, userIDs []string) {
	w.mu.Lock()
	ctx := w.ctx
	w.wg.Add(1)
	w.mu.Unlock()
	go func() {
		defer w.wg.Done()
		ids := make([]string, len(userIDs))
		for i, userID := range userIDs {
			ids[i] = notificationID(windowID, userID)
		}
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		// Rows in user order enter the (user_id, posting_window_id) index
		// mostly sequentially instead of at thousands of random leaf pages.
		if _, err := db.ExecContext(ctx, `INSERT INTO notifications (id, user_id, posting_window_id, created_at)
          SELECT n.id, n.user_id, $3, $4 FROM unnest($1::text[], $2::text[]) AS n(id, user_id) ORDER BY n.user_id
          ON CONFLICT DO NOTHING`, ids, userIDs, windowID, startsAt); err != nil && ctx.Err() == nil {
			slog.Error("notification rows", "window", windowID, "error", err)
		}
	}()
}

// drain stops pending writes and waits for them, before a reset.
func (w *notificationWriter) drain() {
	w.mu.Lock()
	w.cancel()
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.mu.Unlock()
	w.wg.Wait()
}

func openPostingWindowHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input openPostingWindowRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		region := input.Region
		localDate, err := time.Parse(time.DateOnly, input.LocalDate)
		if !validRegion(region) || err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_posting_window")
		}
		seconds := int64(24 * 60 * 60)
		if input.OnTimeSeconds != nil {
			seconds = *input.OnTimeSeconds
		}
		ctx := c.Request().Context()
		tx, err := h.DB.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Openings of one region are serialized by the unique index on the open
		// window (posting_windows_one_active_region) and by the update of the
		// previous window; posts take FOR SHARE on the open window, so this
		// update waits for in-flight posts of the closing window and new posts
		// see the new one.
		var conflict bool
		if err := tx.GetContext(ctx, &conflict, `SELECT EXISTS(SELECT 1 FROM posting_windows WHERE region = $1 AND local_date >= $2)`, region, localDate); err != nil {
			return err
		}
		if conflict {
			return NewAPIError(http.StatusConflict, "posting_window_conflict")
		}
		var startsAt time.Time
		if err := tx.GetContext(ctx, &startsAt, `SELECT clock_timestamp()`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE posting_windows SET ended_at = $2 WHERE region = $1 AND ended_at IS NULL`, region, startsAt); err != nil {
			return err
		}
		windowID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `
   INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
   VALUES ($1, $2, $3, $4, $5)
  `, windowID, region, localDate, startsAt, startsAt.Add(time.Duration(seconds)*time.Second)); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// A concurrent opening of the region won the unique index.
				return NewAPIError(http.StatusConflict, "posting_window_conflict")
			}
			return err
		}
		// The notified set is the active users of the region in this snapshot.
		var notified []string
		if err := tx.SelectContext(ctx, &notified, `SELECT id FROM users WHERE region = $1 AND status = 'active'`, region); err != nil {
			return err
		}
		window, err := getPostingWindow(ctx, tx, windowID)
		if err != nil {
			return err
		}
		var version int64
		if err := tx.GetContext(ctx, &version, `SELECT nextval('change_seq')`); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		notificationWrites.write(h.DB, windowID, startsAt, notified)
		broadcast(ctx, stateEvent{Version: version, Kind: "window", Window: &hotWindow{
			ID: window.ID, Region: window.Region, LocalDate: window.LocalDate, StartsAt: window.StartsAt,
			OnTimeDeadline: window.OnTimeDeadline, Notified: notified}})
		return c.JSON(http.StatusCreated, openPostingWindowResponse{
			ID: window.ID, Region: window.Region, LocalDate: window.LocalDate.Format(time.DateOnly),
			StartsAt: window.StartsAt, OnTimeDeadline: window.OnTimeDeadline,
		})
	}
}
