package imageurl_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

func TestSignVector(t *testing.T) {
	// webapp/go/image_signature_test.go と同じ値（Python で独立に計算したもの）。
	got := imageurl.Sign("test-secret", "550e8400-e29b-41d4-a716-446655440000", 1790553600)
	if got != "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84" {
		t.Fatalf("Sign = %q", got)
	}
}

func TestVerify(t *testing.T) {
	now := time.Unix(1790553600, 0)
	exp := now.Add(24 * time.Hour).Unix()
	valid := fmt.Sprintf("/api/images/img1?exp=%d&sig=%s", exp, imageurl.Sign("secret", "img1", exp))
	if err := imageurl.Verify(valid, "img1", "secret", now); err != nil {
		t.Fatal(err)
	}
	if imageurl.ImageID(valid) != "img1" {
		t.Fatal("ImageID")
	}
	for name, tc := range map[string]struct {
		url, imageID, secret string
		now                  time.Time
	}{
		"other owner's key": {valid, "img1", "other", now},
		"other image":       {valid, "img2", "secret", now},
		"expired":           {valid, "img1", "secret", now.Add(25 * time.Hour)},
		"too far future":    {valid, "img1", "secret", now.Add(-time.Hour)},
		"tampered exp":      {strings.Replace(valid, fmt.Sprint(exp), fmt.Sprint(exp-1), 1), "img1", "secret", now},
		"other host":        {"http://evil.example" + valid, "img1", "secret", now},
	} {
		if err := imageurl.Verify(tc.url, tc.imageID, tc.secret, tc.now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
