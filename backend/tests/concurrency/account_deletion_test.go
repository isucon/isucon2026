package concurrency_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/isucon/isucon2026/backend/tests/testkit"
)

func TestUS4DeadlinePurgeAndRetry(t *testing.T) {
	pool := testkit.Database(t)
	user := relationUser(t, pool)
	hash, _ := service.HashPassword("password-1")
	_, _ = pool.Exec(context.Background(), `UPDATE users SET password_hash=$2 WHERE id=$1`, user, hash)
	fake := clock.NewFake(concurrencyNow)
	db := &postgres.DB{Pool: pool}
	accounts := postgres.NewAccountRepository(db, fake)
	sessions := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, fake)
	due, err := accounts.RequestDeletion(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	fake.Set(due)
	loginErr, purgeErr := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := service.NewSessionService(sessions).Login(context.Background(), service.LoginInput{Username: "u" + user.String()[:8], Password: "password-1"})
		loginErr <- err
	}()
	go func() { _, err := accounts.Purge(context.Background(), user); purgeErr <- err }()
	if err := <-loginErr; err == nil {
		t.Fatal("login succeeded at purge deadline")
	}
	if err := <-purgeErr; err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Purge(context.Background(), user); err != nil {
		t.Fatalf("repeated purge=%v", err)
	}
}

func TestUS4ImageFinalizeFailureAndGC(t *testing.T) {
	root := t.TempDir()
	store := isurealimage.NewStore(root, passthroughSanitizer{}, domain.RandomIDGenerator{})
	accounts := service.NewAccountService(failingProfileStore{}, noopAccountStore{}, store)
	if err := accounts.SetProfileImage(context.Background(), uuid.New(), bytes.NewReader(smallJPEG(t))); err == nil {
		t.Fatal("DB finalize failure not returned")
	}
	testkit.AssertNoImageFiles(t, root)
	object, err := store.Save(context.Background(), uuid.New(), isurealimage.ProfileImage, bytes.NewReader(smallJPEG(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GC(context.Background(), map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(object.ObjectKey); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
}

type passthroughSanitizer struct{}

func (passthroughSanitizer) Sanitize(_ context.Context, input io.Reader, output io.Writer) error {
	decoded, _, err := image.Decode(input)
	if err != nil {
		return err
	}
	return jpeg.Encode(output, decoded, nil)
}
func smallJPEG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type failingProfileStore struct{}

func (failingProfileStore) Get(context.Context, uuid.UUID) (domain.User, *string, error) {
	return domain.User{}, nil, nil
}
func (failingProfileStore) Update(context.Context, uuid.UUID, domain.ProfileUpdate) (domain.User, *string, error) {
	return domain.User{}, nil, nil
}
func (failingProfileStore) SetImage(context.Context, uuid.UUID, isurealimage.Object) (string, error) {
	return "", errors.New("database finalize failure")
}
func (failingProfileStore) DeleteImage(context.Context, uuid.UUID) (string, error) { return "", nil }
func (failingProfileStore) ProfileImage(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", nil
}

type noopAccountStore struct{}

func (noopAccountStore) RequestDeletion(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}
func (noopAccountStore) DueAccounts(context.Context, int) ([]uuid.UUID, error) { return nil, nil }
func (noopAccountStore) Purge(context.Context, uuid.UUID) ([]string, error)    { return nil, nil }
