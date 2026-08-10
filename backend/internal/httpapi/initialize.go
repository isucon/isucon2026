package httpapi

import (
	"net/http"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

const defaultMigrationDirectory = "migrations"

type InitializeHandler struct {
	pool   *pgxpool.Pool
	clock  *clock.Virtual
	migDir string
}

func NewInitializeHandler(pool *pgxpool.Pool, clk *clock.Virtual) *InitializeHandler {
	return &InitializeHandler{
		pool:   pool,
		clock:  clk,
		migDir: defaultMigrationDirectory,
	}
}

type InitializeResponse struct {
	Language string    `json:"language"`
	BaseTime time.Time `json:"base_time"`
}

type VirtualTimeAdvanceRequest struct {
	OffsetSeconds int64 `json:"offset_seconds"`
}

type VirtualTimeAdvanceResponse struct {
	CurrentTime time.Time `json:"current_time"`
}

func (h *InitializeHandler) Initialize(c *echo.Context) error {
	ctx := c.Request().Context()
	if err := seed.Minimal(ctx, h.pool, h.migDir); err != nil {
		return &AppError{
			Status: http.StatusInternalServerError,
			Code:   "initialize_failed",
			Title:  "Internal Server Error",
			Cause:  err,
		}
	}

	baseTime := seed.FixedTime()
	h.clock.SetBaseTime(baseTime)

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
		BaseTime: baseTime,
	})
}

func (h *InitializeHandler) AdvanceTime(c *echo.Context) error {
	var req VirtualTimeAdvanceRequest
	if err := DecodeJSON(c, &req); err != nil {
		return err
	}
	h.clock.Advance(time.Duration(req.OffsetSeconds) * time.Second)
	return c.JSON(http.StatusOK, VirtualTimeAdvanceResponse{
		CurrentTime: h.clock.Now(),
	})
}
