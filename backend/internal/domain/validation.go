package domain

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidUsername    = errors.New("username must contain 1 to 64 ASCII letters, digits, _, ., or -")
	ErrInvalidPassword    = errors.New("password must contain 8 to 72 printable ASCII characters")
	ErrInvalidDisplayName = errors.New("display name must contain 1 to 100 Unicode code points")
	ErrInvalidBio         = errors.New("bio must contain at most 500 Unicode code points")
	ErrInvalidRegion      = errors.New("region must be america, europe, or asia")
	usernamePattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	defaultCaseFolder     = cases.Fold()
)

func NormalizeUsername(username string) (string, error) {
	if err := ValidateUsername(username); err != nil {
		return "", err
	}
	return strings.ToLower(username), nil
}

func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

func ValidatePassword(password string) error {
	if len(password) < 8 || len(password) > 72 {
		return ErrInvalidPassword
	}
	for index := 0; index < len(password); index++ {
		if password[index] < 0x20 || password[index] > 0x7e {
			return ErrInvalidPassword
		}
	}
	return nil
}

func ValidateDisplayName(displayName string) error {
	if !validCodePointLength(displayName, 1, 100) {
		return ErrInvalidDisplayName
	}
	return nil
}

func ValidateBio(bio string) error {
	if !validCodePointLength(bio, 0, 500) {
		return ErrInvalidBio
	}
	return nil
}

func ValidateRegion(region string) error {
	switch region {
	case "america", "europe", "asia":
		return nil
	default:
		return ErrInvalidRegion
	}
}

func ValidateCodePointLength(value string, minimum, maximum int) bool {
	return validCodePointLength(value, minimum, maximum)
}

func NormalizeSearchText(value string) string {
	normalized := norm.NFC.String(value)
	return norm.NFC.String(defaultCaseFolder.String(normalized))
}

func validCodePointLength(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}
