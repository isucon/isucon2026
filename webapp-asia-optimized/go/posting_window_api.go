package isureal

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type currentPostingWindowResponse struct {
	ID             string    `json:"id"`
	Region         string    `json:"region"`
	LocalDate      string    `json:"localDate"`
	StartsAt       time.Time `json:"startsAt"`
	OnTimeDeadline time.Time `json:"onTimeDeadline"`
}

type getCurrentPostingWindowResponse struct {
	Window        *currentPostingWindowResponse `json:"window"`
	HasPosted     bool                          `json:"hasPosted"`
	CanPost       bool                          `json:"canPost"`
	DeletionCount int                           `json:"deletionCount"`
}

func getCurrentPostingWindowHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		user, err := getUserAnyStatusCached(c.Request().Context(), h.DB, GetSession(c).UserID)
		if err != nil {
			return err
		}
		window, ok := state.currentWindow(user.Region)
		if !ok {
			return c.JSON(http.StatusOK, getCurrentPostingWindowResponse{})
		}
		active, deleted, _ := state.postCounts(user.ID, window.LocalDate)
		return c.JSON(http.StatusOK, getCurrentPostingWindowResponse{
			Window: &currentPostingWindowResponse{
				ID: window.ID, Region: window.Region, LocalDate: window.LocalDate.Format(time.DateOnly),
				StartsAt: window.StartsAt, OnTimeDeadline: window.OnTimeDeadline,
			},
			HasPosted:     active > 0,
			CanPost:       active == 0 && deleted < 2 && !time.Now().Before(window.StartsAt),
			DeletionCount: deleted,
		})
	}
}
