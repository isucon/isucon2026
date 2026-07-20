package domain

import (
	"errors"
	"github.com/google/uuid"
	"regexp"
	"time"
)

var monthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func ValidateMemoryMonth(value string) error {
	if !monthPattern.MatchString(value) {
		return errors.New("invalid memory month")
	}
	return nil
}
func ValidateMemoryDate(value string) (time.Time, error) { return time.Parse("2006-01-02", value) }

type MemoryRecord struct {
	PostRecord PostRecord
	RealMojis  []RealMojiRecord
	Comments   []CommentRecord
}
type MemoryCursor struct {
	PostedAt time.Time
	PostID   uuid.UUID
}
