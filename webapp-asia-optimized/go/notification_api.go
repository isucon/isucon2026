package isureal

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id" db:"id"`
	PostingWindowID string    `json:"postingWindowId" db:"posting_window_id"`
	Region          string    `json:"region" db:"region"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		connectedAt := time.Now()
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			user, err := getUserCached(ctx, h.DB, userID)
			if err != nil {
				if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
					c.Logger().Error("failed to get notification recipient", "error", err)
				}
				return nil
			}
			if user.Region != session.Region {
				return nil
			}
			opened := windowOpenedSignal()
			if notification, ok := state.pendingNotification(userID, connectedAt); ok {
				data, err := json.Marshal(notification)
				if err != nil {
					c.Logger().Error("failed to encode notification", "error", err)
					return nil
				}
				if _, err := fmt.Fprintf(response, "data: %s\n\n", data); err != nil {
					return nil
				}
				_ = controller.Flush()
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			case <-opened:
				// 全員が同時に起きないように 0〜notifySpread の乱数だけ遅らせる
				if notifySpread > 0 {
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(time.Duration(rand.Int64N(int64(notifySpread)))):
					}
				}
			}
		}
	}
}

// 投稿期間が始まったら待機中の通知を起こす（取り逃しを防ぐため、確認の前に現在のチャネルを取る）
var (
	windowSignalMu sync.Mutex
	windowSignal   = make(chan struct{})
	notifySpread   = func() time.Duration {
		ms, _ := strconv.Atoi(os.Getenv("ISUREAL_NOTIFY_SPREAD_MS"))
		return time.Duration(ms) * time.Millisecond
	}()
)

func windowOpenedSignal() <-chan struct{} {
	windowSignalMu.Lock()
	defer windowSignalMu.Unlock()
	return windowSignal
}

func notifyWindowOpened() {
	windowSignalMu.Lock()
	close(windowSignal)
	windowSignal = make(chan struct{})
	windowSignalMu.Unlock()
}
