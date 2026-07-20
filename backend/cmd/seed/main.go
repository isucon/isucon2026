package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucon2026/backend/internal/seed"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	fixture := flag.String("fixture", "minimal", "seed fixture")
	directory := flag.String("dir", "migrations", "migration directory")
	ifEmpty := flag.Bool("if-empty", false, "seed only when the migrated database has no application data")
	flag.Parse()
	if *fixture != "minimal" {
		fmt.Fprintln(os.Stderr, "only the minimal fixture is supported")
		os.Exit(2)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("ISUREAL_DATABASE_URL"))
	if err == nil {
		defer pool.Close()
		if *ifEmpty {
			err = seed.MinimalIfEmpty(ctx, pool, *directory)
		} else {
			err = seed.Minimal(ctx, pool, *directory)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
