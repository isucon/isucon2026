// Command migrate applies the schema once before an ECS release.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/isucon/isucon2026/portal/internal/db"
)

func run(ctx context.Context) error {
	conn, err := db.Open(os.Getenv("ISUXPORTAL_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer conn.Close()
	return db.Migrate(ctx, conn)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("schema applied")
}
