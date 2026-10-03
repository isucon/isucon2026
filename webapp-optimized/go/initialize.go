package isureal

import (
	"context"
	"net/http"
	"sync"

	"os"
	"os/exec"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type initializeResponse struct {
	Language string `json:"language"`
}

func initializeHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		cmd := exec.Command("../sql/init.sh")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		if err := reloadEverywhere(c.Request().Context(), h.DB); err != nil {
			return err
		}
		warmConnections(c.Request().Context(), h.DB)

		return c.JSON(http.StatusOK, initializeResponse{Language: "go"})
	}
}

// warmConnections opens the pooled connections now, so the load does not pay
// for connection setup (several round trips from overseas) on its first requests.
func warmConnections(ctx context.Context, db *sqlx.DB) {
	var wg sync.WaitGroup
	for range db.Stats().MaxOpenConnections {
		wg.Go(func() {
			_, _ = db.ExecContext(ctx, `SELECT pg_sleep(0.05)`)
		})
	}
	wg.Wait()
}
