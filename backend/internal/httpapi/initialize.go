package httpapi

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

const (
	defaultSchemaPath = "schema/schema.sql"
	schemaPathEnvKey  = "ISUREAL_SCHEMA_PATH"
)

// VirtualOffsetStore persists the virtual clock offset so that other
// processes (e.g. the worker) can follow the API's virtual time.
type VirtualOffsetStore interface {
	SaveVirtualOffset(ctx context.Context, offset time.Duration) error
}

type InitializeHandler struct {
	pool       *pgxpool.Pool
	clock      *clock.Virtual
	offsets    VirtualOffsetStore
	schemaPath string
}

func NewInitializeHandler(pool *pgxpool.Pool, clk *clock.Virtual, offsets VirtualOffsetStore) *InitializeHandler {
	schemaPath := os.Getenv(schemaPathEnvKey)
	if schemaPath == "" {
		schemaPath = defaultSchemaPath
	}
	return &InitializeHandler{
		pool:       pool,
		clock:      clk,
		offsets:    offsets,
		schemaPath: schemaPath,
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
	if err := seed.Minimal(ctx, h.pool, h.schemaPath); err != nil {
		return &AppError{
			Status: http.StatusInternalServerError,
			Code:   "initialize_failed",
			Title:  "Internal Server Error",
			Cause:  err,
		}
	}

	baseTime := seed.FixedTime()
	h.clock.SetBaseTime(baseTime)
	if err := h.offsets.SaveVirtualOffset(ctx, h.clock.Offset()); err != nil {
		return &AppError{
			Status: http.StatusInternalServerError,
			Code:   "initialize_failed",
			Title:  "Internal Server Error",
			Cause:  err,
		}
	}

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
	if err := h.offsets.SaveVirtualOffset(c.Request().Context(), h.clock.Offset()); err != nil {
		return &AppError{
			Status: http.StatusInternalServerError,
			Code:   "virtual_time_advance_failed",
			Title:  "Internal Server Error",
			Cause:  err,
		}
	}
	return c.JSON(http.StatusOK, VirtualTimeAdvanceResponse{
		CurrentTime: h.clock.Now(),
	})
}
