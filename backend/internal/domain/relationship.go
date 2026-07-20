package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSelfRelationship  = errors.New("self relationship is not allowed")
	ErrInvalidTransition = errors.New("invalid friend request transition")
)

type UserPair struct{ Low, High uuid.UUID }

func NormalizePair(left, right uuid.UUID) (UserPair, error) {
	if left == right {
		return UserPair{}, ErrSelfRelationship
	}
	if left.String() < right.String() {
		return UserPair{Low: left, High: right}, nil
	}
	return UserPair{Low: right, High: left}, nil
}

type FriendRequestStatus string

const (
	RequestPending    FriendRequestStatus = "pending"
	RequestAccepted   FriendRequestStatus = "accepted"
	RequestRejected   FriendRequestStatus = "rejected"
	RequestCancelled  FriendRequestStatus = "cancelled"
	RequestMutualized FriendRequestStatus = "mutualized"
)

type FriendRequest struct {
	ID, RequesterID, RecipientID uuid.UUID
	Status                       FriendRequestStatus
	CreatedAt, UpdatedAt         time.Time
}

func (r FriendRequest) CanTransition(actor uuid.UUID, next FriendRequestStatus) error {
	if r.Status != RequestPending {
		return ErrInvalidTransition
	}
	switch next {
	case RequestAccepted, RequestRejected:
		if actor == r.RecipientID {
			return nil
		}
	case RequestCancelled:
		if actor == r.RequesterID {
			return nil
		}
	}
	return ErrInvalidTransition
}

func CanFollow(accountType string) bool { return accountType == "certified_public" }

func DiscloseProfile(user User, directFriend bool) User {
	if user.AccountType != "certified_public" && !directFriend {
		user.Bio = ""
	}
	return user
}

type RelationshipKind string

const (
	RelationshipFriends   RelationshipKind = "friends"
	RelationshipFollowing RelationshipKind = "following"
	RelationshipFollowers RelationshipKind = "followers"
	RelationshipIncoming  RelationshipKind = "incoming-requests"
	RelationshipOutgoing  RelationshipKind = "outgoing-requests"
)

func (kind RelationshipKind) Validate() error {
	switch kind {
	case RelationshipFriends, RelationshipFollowing, RelationshipFollowers, RelationshipIncoming, RelationshipOutgoing:
		return nil
	}
	return errors.New("invalid relationship kind")
}

type RelationshipRecord struct {
	RelationID uuid.UUID
	User       User
	Status     string
	UpdatedAt  time.Time
}
