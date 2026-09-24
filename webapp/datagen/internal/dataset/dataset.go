// Package dataset describes the deterministic dummy data that is injected into
// an empty ISUReal database. The data is intentionally expressed as plain Go
// values so that it can be bulk-loaded through the PostgreSQL COPY protocol and
// later round-tripped back into webapp/sql/002-seed.sql by the dump package.
package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FixedTime is the timestamp used for every generated row. Keeping it constant
// makes repeated generator runs produce a byte-for-byte identical seed file.
var FixedTime = time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)

// Administrator credentials for the first version of the fixture.
const (
	AdministratorID       = "00000000-0000-0000-0000-000000000001"
	AdministratorUsername = "admin"
	AdministratorPassword = "password1234"
)

// HashCredential mirrors the application's credential hashing. The webapp
// stores hex(SHA-256(plain)) for both user secret keys and administrator
// passwords, so the generated rows must use the same encoding.
func HashCredential(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Table is a set of rows to bulk-load into a single table.
type Table struct {
	Name    string
	Columns []string
	Rows    [][]any
}

// Dataset is the ordered collection of tables that make up the fixture.
type Dataset struct {
	Tables []Table
}

// Build returns the fixture for the current generator version.
//
// Version 1 only creates the administrator account (admin:password1234).
// Future versions can append further tables here; the dump command discovers
// the schema dynamically, so no other change is required.
func Build() *Dataset {
	return &Dataset{
		Tables: []Table{
			administratorsTable(),
		},
	}
}

func administratorsTable() Table {
	return Table{
		Name:    "administrators",
		Columns: []string{"id", "username", "password_hash", "created_at"},
		Rows: [][]any{
			{
				AdministratorID,
				AdministratorUsername,
				HashCredential(AdministratorPassword),
				FixedTime,
			},
		},
	}
}

// Insert bulk-loads every table of the dataset inside the given transaction
// using the PostgreSQL COPY protocol. COPY avoids per-row round trips and is
// orders of magnitude faster than individual INSERT statements.
func (d *Dataset) Insert(ctx context.Context, tx pgx.Tx) error {
	for _, table := range d.Tables {
		if len(table.Rows) == 0 {
			continue
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{table.Name}, table.Columns, pgx.CopyFromRows(table.Rows)); err != nil {
			return fmt.Errorf("bulk insert into %s: %w", table.Name, err)
		}
	}
	return nil
}
