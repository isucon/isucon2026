package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id" db:"id"`
	PostingWindowID string    `json:"postingWindowId" db:"posting_window_id"`
	Region          string    `json:"region" db:"region"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
}

// notificationID is the ID of the notification of a window to a user. It is
// derived, so every region and the background writer agree on it.
func notificationID(windowID, userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(windowID+":"+userID)).String()
}

// notificationWaiter is a subscribed notification stream, waiting for the next
// window of its region to open.
type notificationWaiter struct {
	userID      string
	region      string
	connectedAt time.Time
	result      chan *getNotificationsResponse // closed to end the stream without a notification
}

// notificationHub delivers the notifications of opening windows to the
// subscribed streams from the hot state, without polling the database. A
// window event reaches every region before the opening responds, so the
// notification is delivered as soon as the window exists.
type notificationHub struct {
	mu      sync.Mutex
	waiters map[string]map[*notificationWaiter]struct{} // by user
}

var notifications = &notificationHub{waiters: map[string]map[*notificationWaiter]struct{}{}}

// subscribe registers a waiter. It is called with the hot state lock not
// held; the window check and the registration are atomic under the hub lock
// so an opening in between is not missed.
func (h *notificationHub) subscribe(w *notificationWaiter, current *hotWindow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current != nil && current.StartsAt.After(w.connectedAt) && current.notifiedSet()[w.userID] {
		w.result <- notificationOf(current, w.userID)
		close(w.result)
		return
	}
	if h.waiters[w.userID] == nil {
		h.waiters[w.userID] = map[*notificationWaiter]struct{}{}
	}
	h.waiters[w.userID][w] = struct{}{}
}

func (h *notificationHub) unsubscribe(w *notificationWaiter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(w)
}

func (h *notificationHub) removeLocked(w *notificationWaiter) {
	if set := h.waiters[w.userID]; set != nil {
		delete(set, w)
		if len(set) == 0 {
			delete(h.waiters, w.userID)
		}
	}
}

func notificationOf(w *hotWindow, userID string) *getNotificationsResponse {
	return &getNotificationsResponse{ID: notificationID(w.ID, userID), PostingWindowID: w.ID, Region: w.Region, CreatedAt: w.StartsAt}
}

// windowOpened delivers the window's notification to the waiting streams of
// the notified users that connected before it opened.
func (h *notificationHub) windowOpened(w *hotWindow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, userID := range w.Notified {
		for waiter := range h.waiters[userID] {
			if waiter.region == w.Region && w.StartsAt.After(waiter.connectedAt) {
				waiter.result <- notificationOf(w, userID)
				close(waiter.result)
				h.removeLocked(waiter)
			}
		}
	}
}

// userChanged ends the streams of a user who left the region or the service.
func (h *notificationHub) userChanged(userID, region string, active bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for waiter := range h.waiters[userID] {
		if !active || waiter.region != region {
			close(waiter.result)
			h.removeLocked(waiter)
		}
	}
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		connectedAt := time.Now()
		session := GetSession(c)
		user, known := hotSessionUser(c, h)
		if !known {
			// The state is not loaded yet: authenticate through the database.
			var err error
			if user, err = readSessionUser(c, h); err != nil {
				return err
			}
		}
		if user.Region != h.Config.Region {
			return NewAPIError(http.StatusForbidden, "region_mismatch")
		}
		if _, err := imageSigningKey(user.SecretKeyHash); err != nil {
			return err
		}
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}
		waiter := &notificationWaiter{userID: user.ID, region: session.Region, connectedAt: connectedAt, result: make(chan *getNotificationsResponse, 1)}
		current, _, _, _ := hot.currentWindow(user.ID)
		notifications.subscribe(waiter, current)
		defer notifications.unsubscribe(waiter)
		select {
		case <-ctx.Done():
			return nil
		case notification, ok := <-waiter.result:
			if !ok || notification == nil {
				return nil
			}
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
	}
}
