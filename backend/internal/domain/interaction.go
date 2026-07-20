package domain

import (
	"errors"
	"github.com/google/uuid"
	"time"
	"unicode/utf8"
)

type ReactionType string

const (
	ReactionLike     ReactionType = "like"
	ReactionSmile    ReactionType = "smile"
	ReactionSurprise ReactionType = "surprise"
	ReactionHeart    ReactionType = "heart"
	ReactionLaugh    ReactionType = "laugh"
	ReactionInstant  ReactionType = "instant"
)

func (r ReactionType) Validate() error {
	switch r {
	case ReactionLike, ReactionSmile, ReactionSurprise, ReactionHeart, ReactionLaugh, ReactionInstant:
		return nil
	}
	return errors.New("invalid reaction type")
}
func CanCreateRealMoji(reactor, author uuid.UUID) bool { return reactor != author }
func ValidateComment(body string) bool {
	return utf8.ValidString(body) && utf8.RuneCountInString(body) >= 1 && utf8.RuneCountInString(body) <= 1000
}

type RealMoji struct {
	ID, PostID, ReactorID, ImageID uuid.UUID
	ReactionType                   ReactionType
	CreatedAt                      time.Time
}
type Comment struct {
	ID, PostID, AuthorID uuid.UUID
	Body                 string
	CreatedAt            time.Time
}
type RealMojiRecord struct {
	RealMoji RealMoji
	Reactor  User
}
type CommentRecord struct {
	Comment Comment
	Author  User
}
