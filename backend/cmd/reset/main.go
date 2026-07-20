package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucon2026/backend/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	directory := flag.String("dir", "migrations", "migration directory")
	flag.Parse()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("ISUREAL_DATABASE_URL"))
	if err == nil {
		defer pool.Close()
		err = migrate.Reset(ctx, pool, *directory)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
