package isureal

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

const ipBanReloadInterval = 200 * time.Millisecond

// IPBanList holds the bans read from ip_bans. A background loop reloads it, so
// requests check bans in memory instead of waiting for the database.
type IPBanList struct {
	reloading sync.Mutex
	bans      atomic.Pointer[map[string]time.Time]
}

type ipBan struct {
	IP          string    `db:"ip"`
	BannedUntil time.Time `db:"banned_until"`
}

func (l *IPBanList) Banned(ip string) bool {
	bans := l.bans.Load()
	if bans == nil {
		return false
	}
	until, ok := (*bans)[ip]
	return ok && time.Now().Before(until)
}

// reload replaces the list. Reloads are serialized so that a slow reload
// started earlier cannot overwrite a newer list, such as the one read right
// after initialization.
func (l *IPBanList) reload(ctx context.Context, db *sqlx.DB) error {
	l.reloading.Lock()
	defer l.reloading.Unlock()
	var rows []ipBan
	if err := db.SelectContext(ctx, &rows, `SELECT ip, banned_until FROM ip_bans WHERE banned_until > clock_timestamp()`); err != nil {
		return err
	}
	bans := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		bans[row.IP] = row.BannedUntil
	}
	l.bans.Store(&bans)
	return nil
}

// run reloads the list at a fixed interval. When a reload outlasts the interval
// (a distant database), missed ticks are dropped instead of queued. A failed
// reload keeps the previous list.
func (l *IPBanList) run(ctx context.Context, db *sqlx.DB) {
	ticker := time.NewTicker(ipBanReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = l.reload(ctx, db)
		}
	}
}

func extractClientIP(req *http.Request) string {
	if ip := req.Header.Get("X-Client-IP"); ip != "" {
		return ip
	}
	return echo.ExtractIPDirect()(req)
}

// IPBanMiddleware rejects banned clients on every API. Requests between the
// regional apps (/internal/) are not client requests.
func IPBanMiddleware(h *HandlerContext) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !strings.HasPrefix(c.Request().URL.Path, "/internal/") && h.IPBans.Banned(c.RealIP()) {
				return NewAPIError(http.StatusForbidden, "ip_banned")
			}
			return next(c)
		}
	}
}
