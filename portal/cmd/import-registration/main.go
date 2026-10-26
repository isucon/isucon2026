// import-registration validates a sanitized Django snapshot and optionally imports it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/registrationmigration"
)

func main() {
	input := flag.String("snapshot", "", "sanitized snapshot JSON file or private s3://bucket/key")
	apply := flag.Bool("apply", false, "import into an empty ISUXPORTAL_DATABASE_URL (default: validation only)")
	rehearsal := flag.String("create-rehearsal-database", "", "create a fresh rehearsal_* database on the configured server and import there (requires -apply)")
	verify := flag.Bool("verify-only", false, "compare destination records with the snapshot without writing")
	flag.Parse()
	if err := run(*input, *apply, *rehearsal, *verify); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string, apply bool, rehearsal string, verify bool) error {
	if verify && (apply || rehearsal != "") {
		return fmt.Errorf("-verify-only cannot be combined with writes")
	}
	if rehearsal != "" {
		if !apply {
			return fmt.Errorf("-create-rehearsal-database requires -apply")
		}
		if err := validateRehearsalDatabase(rehearsal); err != nil {
			return err
		}
	}
	ctx := context.Background()
	file, err := openSnapshot(ctx, path)
	if err != nil {
		return err
	}
	defer file.Close()
	snapshot, err := registrationmigration.Decode(file)
	if err != nil {
		return err
	}
	if apply || verify {
		databaseURL := os.Getenv("ISUXPORTAL_DATABASE_URL")
		if rehearsal != "" {
			databaseURL, err = createRehearsalDatabase(ctx, databaseURL, rehearsal)
			if err != nil {
				return err
			}
		}
		conn, err := db.Open(databaseURL)
		if err != nil {
			return err
		}
		defer conn.Close()
		if apply {
			if err := db.Migrate(ctx, conn); err != nil {
				return err
			}
			if err := snapshot.Import(ctx, conn); err != nil {
				return err
			}
		}
		if err := snapshot.Verify(ctx, conn); err != nil {
			return err
		}
	}
	fmt.Printf("validated teams=%d users=%d applied=%t verified=%t\n", len(snapshot.Teams), len(snapshot.Users), apply, apply || verify)
	return nil
}

func openSnapshot(ctx context.Context, path string) (io.ReadCloser, error) {
	if !strings.HasPrefix(path, "s3://") {
		return os.Open(path)
	}
	u, err := url.Parse(path)
	if err != nil || u.Host == "" || len(u.Path) <= 1 || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("snapshot must be s3://bucket/key without credentials or query")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })
	result, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(u.Host), Key: aws.String(strings.TrimPrefix(u.Path, "/"))})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func validateRehearsalDatabase(name string) error {
	if !regexp.MustCompile(`^rehearsal_[a-z0-9_]{1,53}$`).MatchString(name) {
		return fmt.Errorf("new database must have a rehearsal_ prefix and only lowercase letters, digits or underscores (maximum 63 characters)")
	}
	return nil
}

func createRehearsalDatabase(ctx context.Context, databaseURL, name string) (string, error) {
	if err := validateRehearsalDatabase(name); err != nil {
		return "", err
	}
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("ISUXPORTAL_DATABASE_URL must be a PostgreSQL URL")
	}
	u.Path = "/postgres"
	admin, err := db.Open(u.String())
	if err != nil {
		return "", err
	}
	defer admin.Close()
	// CREATE DATABASE deliberately fails if the destination already exists.
	// No data or schema in the original application database is changed.
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
