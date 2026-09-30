// import-registration validates a sanitized Django snapshot and optionally imports it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/registrationmigration"
)

func main() {
	input := flag.String("snapshot", "", "sanitized snapshot JSON")
	apply := flag.Bool("apply", false, "import into an empty ISUXPORTAL_DATABASE_URL (default: validation only)")
	flag.Parse()
	if err := run(*input, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string, apply bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	snapshot, err := registrationmigration.Decode(file)
	if err != nil {
		return err
	}
	if apply {
		conn, err := db.Open(os.Getenv("ISUXPORTAL_DATABASE_URL"))
		if err != nil {
			return err
		}
		defer conn.Close()
		ctx := context.Background()
		if err := db.Migrate(ctx, conn); err != nil {
			return err
		}
		if err := snapshot.Import(ctx, conn); err != nil {
			return err
		}
	}
	fmt.Printf("validated teams=%d users=%d applied=%t\n", len(snapshot.Teams), len(snapshot.Users), apply)
	return nil
}
