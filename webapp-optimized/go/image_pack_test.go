package isureal

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// pack-images writes the initial data's JPEG images, and the pack returns the
// same bytes one at a time and in batches.
func TestImagePackRoundTrip(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx-auth", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	exec(`DROP TABLE IF EXISTS initial_data_boundary`)

	images := map[string][]byte{}
	var ids []string
	for i := range 3 {
		owner := uuid.NewString()
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,'u','9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`, owner, fmt.Sprintf("packuser%d", i))
		for j := range 5 {
			id := uuid.NewString()
			data := bytes.Repeat([]byte{byte(i), byte(j)}, 1000+i*7+j)
			exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type,created_at) VALUES($1,$2,$3,'image/jpeg',$4)`, id, owner, data, fmt.Sprintf("2026-0%d-1%d", 6+j%3, j))
			images[id] = data
			ids = append(ids, id)
		}
		// Not packed: another media type.
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'png','image/png')`, uuid.NewString(), owner)
	}
	exec(`CREATE TABLE initial_data_boundary AS SELECT clock_timestamp() AS created_at`)
	defer exec(`DROP TABLE IF EXISTS initial_data_boundary`)
	// Not packed: uploaded after the initial data.
	late := uuid.NewString()
	exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) SELECT $1,id,'late','image/jpeg' FROM users LIMIT 1`, late)

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	config := &Config{Region: "asia", DBHost: u.Hostname(), DBPort: u.Port(), DBUser: u.User.Username(), DBPassword: password,
		DBName: strings.TrimPrefix(u.Path, "/")}
	dir := t.TempDir()
	ctx := context.Background()
	if err := BuildImagePack(ctx, config, dir); err != nil {
		t.Fatal(err)
	}
	p, err := openImagePack(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer p.file.Close()
	if len(p.entries) != len(images) {
		t.Fatalf("packed %d images, want %d", len(p.entries), len(images))
	}
	for id, want := range images {
		e, ok := p.lookup(id)
		if !ok {
			t.Fatalf("%s not packed", id)
		}
		got, err := p.read(e)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, ok := p.lookup(late); ok {
		t.Fatal("an image after the boundary was packed")
	}
	batch, err := p.readMany(append(ids, late, "not-an-id"))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != len(images) {
		t.Fatalf("batch read %d images", len(batch))
	}
	for id, got := range batch {
		if !bytes.Equal(got, images[id]) {
			t.Fatalf("%s differs in a batch read", id)
		}
	}
	// A pack built for other initial data is not used.
	exec(`UPDATE initial_data_boundary SET created_at=created_at+interval '1 second'`)
	if _, err := openImagePack(ctx, db, dir); err == nil {
		t.Fatal("a pack of another boundary was opened")
	}
}
