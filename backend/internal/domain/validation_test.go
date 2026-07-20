package domain_test

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/domain"
)

func TestNormalizeUsername(t *testing.T) {
	t.Parallel()
	got, err := domain.NormalizeUsername("Alice.NAME-1")
	if err != nil || got != "alice.name-1" {
		t.Fatalf("NormalizeUsername() = %q, %v", got, err)
	}
}

func TestValidateUsernameBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		valid       bool
	}{
		{"minimum", "a", true}, {"maximum", strings.Repeat("a", 64), true},
		{"empty", "", false}, {"too long", strings.Repeat("a", 65), false},
		{"unicode", "ユーザー", false}, {"space", "alice name", false},
		{"punctuation", "alice_name.test-1", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := domain.ValidateUsername(test.value); (err == nil) != test.valid {
				t.Fatalf("ValidateUsername(%q) error = %v", test.value, err)
			}
		})
	}
}

func TestValidatePasswordUsesCharactersAndPreservesSpaces(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		valid       bool
	}{
		{"minimum", "12345678", true}, {"leading space", " 1234567", true},
		{"maximum", strings.Repeat("~", 72), true}, {"short", "1234567", false},
		{"long", strings.Repeat("a", 73), false}, {"newline", "1234567\n", false},
		{"unicode", "password猫", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := domain.ValidatePassword(test.value); (err == nil) != test.valid {
				t.Fatalf("ValidatePassword(%q) error = %v", test.value, err)
			}
		})
	}
}

func TestUnicodeCodePointLimits(t *testing.T) {
	t.Parallel()
	if err := domain.ValidateDisplayName(strings.Repeat("界", 100)); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateDisplayName(strings.Repeat("界", 101)); err == nil {
		t.Fatal("101 code points must fail")
	}
	if err := domain.ValidateDisplayName(""); err == nil {
		t.Fatal("empty display name must fail")
	}
	if err := domain.ValidateBio(strings.Repeat("🙂", 500)); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateBio(strings.Repeat("🙂", 501)); err == nil {
		t.Fatal("501 code points must fail")
	}
}

func TestNormalizeSearchTextUsesNFCAndDefaultCaseFolding(t *testing.T) {
	t.Parallel()
	if left, right := domain.NormalizeSearchText("Cafe\u0301 STRASSE"), domain.NormalizeSearchText("CAFÉ Straße"); left != right {
		t.Fatalf("case-folded NFC values differ: %q != %q", left, right)
	}
}

func TestValidateRegistrationRegion(t *testing.T) {
	t.Parallel()
	for _, region := range []string{"america", "europe", "asia"} {
		if err := domain.ValidateRegion(region); err != nil {
			t.Fatalf("%s: %v", region, err)
		}
	}
	for _, region := range []string{"", "utc", "Asia"} {
		if err := domain.ValidateRegion(region); err == nil {
			t.Fatalf("%q must fail", region)
		}
	}
}
