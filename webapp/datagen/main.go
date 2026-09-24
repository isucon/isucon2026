// Command datagen generates the ISUReal initial data.
//
// It has two independent phases:
//
//	generate  applies webapp/sql/001-schema.sql to a fresh database and
//	          bulk-loads the deterministic fixture through COPY.
//	dump      reads the database back and writes webapp/sql/002-seed.sql using
//	          multi-row bulk INSERT statements.
//
// "all" runs generate followed by dump, which is the usual workflow:
//
//	cd webapp/datagen && go run . all
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"example.com/isureal-company/datagen/internal/dataset"
	"example.com/isureal-company/datagen/internal/dump"
)

const usage = `datagen generates the ISUReal initial data.

Usage:
  datagen <command> [flags]

Commands:
  generate   Apply 001-schema.sql and bulk-load the dummy data into the database.
  dump       Read the database and write 002-seed.sql (multi-row bulk INSERTs).
  all        Run generate, then dump.

Flags (defaults assume you run from webapp/datagen):
  -dsn string      PostgreSQL connection string
                   (default: ISUREAL_DATABASE_URL, otherwise ISUREAL_DB_* env)
  -schema string   path to 001-schema.sql (default "../sql/001-schema.sql")
  -out string      path to write 002-seed.sql (default "../sql/002-seed.sql")
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, usage)
		return nil
	}
	command := args[0]
	switch command {
	case "generate", "dump", "all":
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}

	fs := flag.NewFlagSet(command, flag.ExitOnError)
	dsnFlag := fs.String("dsn", "", "PostgreSQL connection string")
	schemaPath := fs.String("schema", filepath.Join("..", "sql", "001-schema.sql"), "path to 001-schema.sql")
	outPath := fs.String("out", filepath.Join("..", "sql", "002-seed.sql"), "path to write 002-seed.sql")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	dsn, err := resolveDSN(*dsnFlag)
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	switch command {
	case "generate":
		return generate(ctx, conn, *schemaPath)
	case "dump":
		return dumpToFile(ctx, conn, *outPath)
	default:
		if err := generate(ctx, conn, *schemaPath); err != nil {
			return err
		}
		return dumpToFile(ctx, conn, *outPath)
	}
}

// generate resets the database to 001-schema.sql and bulk-loads the fixture.
func generate(ctx context.Context, conn *pgx.Conn, schemaPath string) error {
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(schema)); err != nil {
		return fmt.Errorf("apply schema %s: %w", schemaPath, err)
	}
	fixture := dataset.Build()
	if err := fixture.Insert(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(os.Stderr, "generated %d table(s) from %s\n", len(fixture.Tables), schemaPath)
	return nil
}

func dumpToFile(ctx context.Context, conn *pgx.Conn, outPath string) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".002-seed-*.sql")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	buffered := bufio.NewWriter(tmp)
	if err := dump.Dump(ctx, conn, buffered); err != nil {
		tmp.Close()
		return err
	}
	if err := buffered.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return fmt.Errorf("replace %s: %w", outPath, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", outPath)
	return nil
}

func connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	// Pin the session formatting so dumped values are stable and portable.
	for _, stmt := range []string{
		`SET TimeZone = 'UTC'`,
		`SET DateStyle = 'ISO, MDY'`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			conn.Close(ctx)
			return nil, fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return conn, nil
}

func resolveDSN(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if dsn := os.Getenv("ISUREAL_DATABASE_URL"); dsn != "" {
		return dsn, nil
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(envDefault("ISUREAL_DB_USER", "isureal"), envDefault("ISUREAL_DB_PASSWORD", "isureal")),
		Host:   net.JoinHostPort(envDefault("ISUREAL_DB_HOST", "127.0.0.1"), envDefault("ISUREAL_DB_PORT", "5432")),
		Path:   "/" + envDefault("ISUREAL_DB_NAME", "isureal"),
	}
	return u.String(), nil
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
