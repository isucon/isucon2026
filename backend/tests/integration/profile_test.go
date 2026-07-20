package integration_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"testing"
)

func TestUS4ProfileAndImageLifecycle(t *testing.T) {
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "america")
	fake := clock.NewFake(us1Now)
	repository := postgres.NewProfileRepository(&postgres.DB{Pool: pool}, fake)
	store := isurealimage.NewStore(t.TempDir(), reencodeSanitizer{}, domain.RandomIDGenerator{})
	accounts := service.NewAccountService(repository, postgres.NewAccountRepository(&postgres.DB{Pool: pool}, fake), store)
	display, bio := "表示名", "自己紹介"
	updated, _, err := accounts.Update(context.Background(), user, domain.ProfileUpdate{DisplayName: &display, Bio: &bio})
	if err != nil || updated.DisplayName != display || updated.Bio != bio {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	invalid := ""
	if _, _, err := accounts.Update(context.Background(), user, domain.ProfileUpdate{DisplayName: &invalid}); err == nil {
		t.Fatal("invalid profile accepted")
	}
	unchanged, _, _ := accounts.Get(context.Background(), user)
	if unchanged.DisplayName != display {
		t.Fatal("invalid update was not atomic")
	}
	if err := accounts.SetProfileImage(context.Background(), user, bytes.NewReader(testJPEG(t, 64, 64))); err != nil {
		t.Fatal(err)
	}
	withImage, _, _ := accounts.Get(context.Background(), user)
	if withImage.ProfileImageID == nil {
		t.Fatal("profile image missing")
	}
	if err := accounts.DeleteProfileImage(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	without, _, _ := accounts.Get(context.Background(), user)
	if without.ProfileImageID != nil {
		t.Fatal("profile image not cleared")
	}
}
func TestUS4ProfileImageBoundaries(t *testing.T) {
	store := isurealimage.NewStore(t.TempDir(), copySanitizer{}, domain.RandomIDGenerator{})
	payload := make([]byte, (2<<20)+1)
	payload[0], payload[1] = 0xff, 0xd8
	if _, err := store.Save(context.Background(), uuid.New(), isurealimage.ProfileImage, bytes.NewReader(payload)); !errors.Is(err, isurealimage.ErrPayloadTooLarge) {
		t.Fatalf("limit=%v", err)
	}
}
