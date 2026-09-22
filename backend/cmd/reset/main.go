package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucon2026/backend/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	schemaPath := flag.String("schema", "schema/schema.sql", "path to schema.sql")
	flag.Parse()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("ISUREAL_DATABASE_URL"))
	if err == nil {
		defer pool.Close()
		err = schema.Reset(ctx, pool, *schemaPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
