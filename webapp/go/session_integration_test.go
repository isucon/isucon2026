package isureal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestCreateLoginSessionIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ('user1', 'alice', '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', 'Alice', 'asia')`); err != nil {
		t.Fatal(err)
	}
	first, err := CreateLoginSession(db, "user1")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(first.Token))
	if first.Token == "" || first.TokenHash != hex.EncodeToString(hash[:]) || first.CreatedAt.IsZero() {
		t.Fatalf("invalid session: %+v", first)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := CreateLoginSession(db, "user1"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var active, total int
	if err := db.Get(&active, `SELECT count(*) FROM login_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&total, `SELECT count(*) FROM login_sessions`); err != nil {
		t.Fatal(err)
	}
	if active != 1 || total != 9 {
		t.Fatalf("active=%d total=%d", active, total)
	}
	var firstActive bool
	if err := db.Get(&firstActive, `SELECT active FROM login_sessions WHERE token_hash=$1`, first.TokenHash); err != nil {
		t.Fatal(err)
	}
	if firstActive {
		t.Fatal("old session is still active")
	}
	if _, err := CreateLoginSession(db, "missing"); err == nil {
		t.Fatal("missing user accepted")
	}
	var currentHash string
	if err := db.Get(&currentHash, `SELECT token_hash FROM login_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE FUNCTION reject_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'insertion rejected'; END $$; CREATE TRIGGER reject_session BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_session()`); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLoginSession(db, "user1"); err == nil {
		t.Fatal("expected insertion failure")
	}
	var afterHash string
	if err := db.Get(&afterHash, `SELECT token_hash FROM login_sessions WHERE active`); err != nil {
		t.Fatal(err)
	}
	if afterHash != currentHash {
		t.Fatal("failed creation changed active session")
	}
}
