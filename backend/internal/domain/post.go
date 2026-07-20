package domain

import (
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Region string

const (
	RegionAmerica Region = "america"
	RegionEurope  Region = "europe"
	RegionAsia    Region = "asia"
)

type PostingWindow struct {
	ID               uuid.UUID
	Region           Region
	LocalDate        time.Time
	UTCOffsetMinutes int
	StartsAt         time.Time
	OnTimeDeadline   time.Time
	EndedAt          *time.Time
}

func ClassifyPost(window PostingWindow, postedAt time.Time) (bool, int64, error) {
	if postedAt.Before(window.StartsAt) {
		return false, 0, errors.New("post precedes posting window")
	}
	if window.EndedAt != nil && !postedAt.Before(*window.EndedAt) {
		return false, 0, errors.New("posting window has ended")
	}
	if !postedAt.After(window.OnTimeDeadline) {
		return true, 0, nil
	}
	return false, int64(postedAt.Sub(window.OnTimeDeadline) / time.Second), nil
}

func CurrentPostingWindow(windows []PostingWindow, region Region) (PostingWindow, bool) {
	var selected PostingWindow
	found := false
	for _, window := range windows {
		if window.Region != region || window.EndedAt != nil {
			continue
		}
		if !found || window.StartsAt.After(selected.StartsAt) {
			selected, found = window, true
		}
	}
	return selected, found
}

type Visibility string

const (
	VisibilityFriends          Visibility = "friends"
	VisibilityFriendsOfFriends Visibility = "friends_of_friends"
	VisibilityPublic           Visibility = "public"
)

func (visibility Visibility) Validate() error {
	switch visibility {
	case VisibilityFriends, VisibilityFriendsOfFriends, VisibilityPublic:
		return nil
	default:
		return errors.New("invalid visibility")
	}
}

type RelationshipDistance int

const (
	Unrelated RelationshipDistance = iota
	DirectFriend
	DistanceTwo
)

func VisibilityAllows(visibility Visibility, relationship RelationshipDistance) bool {
	switch visibility {
	case VisibilityFriends:
		return relationship == DirectFriend
	case VisibilityFriendsOfFriends:
		return relationship == DirectFriend || relationship == DistanceTwo
	case VisibilityPublic:
		return true
	default:
		return false
	}
}

type FeedKind string

const (
	FeedFriends          FeedKind = "friends"
	FeedFriendsOfFriends FeedKind = "friends-of-friends"
	FeedPublic           FeedKind = "public"
	FeedFollowing        FeedKind = "following"
)

func MatchesFeed(feed FeedKind, relationship RelationshipDistance, followed bool) bool {
	switch feed {
	case FeedFriends:
		return relationship == DirectFriend
	case FeedFriendsOfFriends:
		return relationship == DistanceTwo
	case FeedPublic:
		return true
	case FeedFollowing:
		return followed
	default:
		return false
	}
}

func CanComment(visibility Visibility, relationship RelationshipDistance, author bool) bool {
	return author || relationship == DirectFriend || visibility == VisibilityPublic
}

type LocationKind string

const (
	LocationNone        LocationKind = "none"
	LocationExact       LocationKind = "exact"
	LocationApproximate LocationKind = "approximate"
)

type Location struct {
	Kind      LocationKind `json:"kind"`
	Latitude  *float64     `json:"latitude,omitempty"`
	Longitude *float64     `json:"longitude,omitempty"`
	Locality  *string      `json:"locality,omitempty"`
}

func NoLocation() Location { return Location{Kind: LocationNone} }

func ExactLocation(latitude, longitude float64) (Location, error) {
	location := Location{Kind: LocationExact, Latitude: &latitude, Longitude: &longitude}
	return location, location.Validate()
}

func ApproximateLocation(locality string) (Location, error) {
	location := Location{Kind: LocationApproximate, Locality: &locality}
	return location, location.Validate()
}

func (location Location) Validate() error {
	switch location.Kind {
	case LocationNone:
		if location.Latitude != nil || location.Longitude != nil || location.Locality != nil {
			return errors.New("none location contains data")
		}
	case LocationExact:
		if location.Latitude == nil || location.Longitude == nil || location.Locality != nil || math.IsNaN(*location.Latitude) || math.IsInf(*location.Latitude, 0) || math.IsNaN(*location.Longitude) || math.IsInf(*location.Longitude, 0) || *location.Latitude < -90 || *location.Latitude > 90 || *location.Longitude < -180 || *location.Longitude > 180 {
			return errors.New("invalid exact location")
		}
	case LocationApproximate:
		if location.Latitude != nil || location.Longitude != nil || location.Locality == nil || !utf8.ValidString(*location.Locality) || utf8.RuneCountInString(*location.Locality) < 1 || utf8.RuneCountInString(*location.Locality) > 100 {
			return errors.New("invalid approximate location")
		}
	default:
		return errors.New("invalid location kind")
	}
	return nil
}

type Post struct {
	ID, AuthorID, PostingWindowID, ImageID uuid.UUID
	Caption                                string
	Visibility                             Visibility
	Location                               Location
	PostedAt                               time.Time
	OnTime                                 bool
	LateSeconds                            int64
	DeletedAt                              *time.Time
}

type PostRecord struct {
	Post   Post
	Author User
}

type FeedQuery struct {
	ViewerID       uuid.UUID
	Kind           FeedKind
	BeforePostedAt *time.Time
	BeforeID       *uuid.UUID
	Limit          int
}

type PostSlot struct {
	ActivePostID  *uuid.UUID
	DeletionCount int
	PostedAt      *time.Time
}

// VisibilityFacts is the complete, content-free authorization input for a post.
// Keeping it in the domain package lets repositories provide facts without
// depending on the service layer that evaluates them.
type VisibilityFacts struct {
	ViewerID, AuthorID         uuid.UUID
	ViewerActive, AuthorActive bool
	Blocked, ViewerPosted      bool
	PostCurrent, Followed      bool
	Relationship               RelationshipDistance
	Visibility                 Visibility
}

func (slot PostSlot) CanPost() bool { return slot.ActivePostID == nil && slot.DeletionCount < 2 }

func (slot PostSlot) WithPost(postID uuid.UUID, postedAt time.Time) (PostSlot, error) {
	if !slot.CanPost() {
		return slot, errors.New("post slot is unavailable")
	}
	slot.ActivePostID = &postID
	slot.PostedAt = &postedAt
	return slot, nil
}

func (slot PostSlot) WithoutPost(postID uuid.UUID) (PostSlot, error) {
	if slot.ActivePostID == nil || *slot.ActivePostID != postID {
		return slot, errors.New("post is not active")
	}
	slot.ActivePostID = nil
	slot.DeletionCount++
	if slot.DeletionCount > 2 {
		return slot, errors.New("post deletion limit exceeded")
	}
	return slot, nil
}

func ValidateCaption(caption string) error {
	if !utf8.ValidString(caption) || utf8.RuneCountInString(caption) > 1000 {
		return errors.New("caption must contain at most 1000 code points")
	}
	return nil
}
