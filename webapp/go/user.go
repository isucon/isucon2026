package isureal

import (
	"context"
	"regexp"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
)

type User struct {
	ID             string  `db:"id" json:"id"`
	Username       string  `db:"username" json:"username"`
	SecretKeyHash  string  `db:"secret_key_hash" json:"-"`
	DisplayName    string  `db:"display_name" json:"displayName"`
	Bio            string  `db:"bio" json:"bio"`
	ProfileImageID *string `db:"profile_image_id" json:"-"`
	Region         string  `db:"region" json:"region"`
	AccountType    string  `db:"account_type" json:"accountType"`
	Status         string  `db:"status" json:"status"`
	ImageKey       []byte  `db:"-" json:"-"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

func validUserText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum
}

func validRegion(region string) bool {
	switch region {
	case "america", "europe", "asia":
		return true
	default:
		return false
	}
}

func getUser(ctx context.Context, db sqlx.QueryerContext, userID string) (User, error) {
	var user User
	err := sqlx.GetContext(ctx, db, &user, `SELECT id, username, secret_key_hash, display_name, bio, profile_image_id, region, account_type, status FROM users WHERE id = $1 AND status = 'active'`, userID)
	if err != nil {
		return user, err
	}
	user.ImageKey, err = imageSigningKey(user.SecretKeyHash)
	return user, err
}
