package domain_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

func TestTwoMinutePostClassificationIsInclusive(t *testing.T) {
	t.Parallel()
	starts := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	window := domain.PostingWindow{StartsAt: starts, OnTimeDeadline: starts.Add(2 * time.Minute)}
	for _, instant := range []time.Time{starts, starts.Add(2 * time.Minute)} {
		onTime, late, err := domain.ClassifyPost(window, instant)
		if err != nil || !onTime || late != 0 {
			t.Fatalf("ClassifyPost(%s) = %v,%d,%v", instant, onTime, late, err)
		}
	}
	onTime, late, err := domain.ClassifyPost(window, starts.Add(2*time.Minute+1500*time.Millisecond))
	if err != nil || onTime || late != 1 {
		t.Fatalf("late classification = %v,%d,%v", onTime, late, err)
	}
}

func TestCurrentWindowSelectionUsesUsersFixedRegion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Hour)
	windows := []domain.PostingWindow{
		{ID: uuid.New(), Region: domain.RegionAsia, StartsAt: now.Add(-time.Hour)},
		{ID: uuid.New(), Region: domain.RegionEurope, StartsAt: now.Add(-2 * time.Hour)},
		{ID: uuid.New(), Region: domain.RegionAsia, StartsAt: now.Add(-24 * time.Hour), EndedAt: &ended},
	}
	selected, ok := domain.CurrentPostingWindow(windows, domain.RegionEurope)
	if !ok || selected.ID != windows[1].ID {
		t.Fatalf("selected = %#v, %v", selected, ok)
	}
}

func TestVisibilityAndFeedKindsAreDistinct(t *testing.T) {
	t.Parallel()
	relations := []struct {
		name                 string
		relation             domain.RelationshipDistance
		friends, fof, public bool
	}{
		{"direct", domain.DirectFriend, true, true, true},
		{"distance two", domain.DistanceTwo, false, true, true},
		{"unrelated", domain.Unrelated, false, false, true},
	}
	for _, relation := range relations {
		if got := domain.VisibilityAllows(domain.VisibilityFriends, relation.relation); got != relation.friends {
			t.Errorf("friends/%s = %v", relation.name, got)
		}
		if got := domain.VisibilityAllows(domain.VisibilityFriendsOfFriends, relation.relation); got != relation.fof {
			t.Errorf("fof/%s = %v", relation.name, got)
		}
		if got := domain.VisibilityAllows(domain.VisibilityPublic, relation.relation); got != relation.public {
			t.Errorf("public/%s = %v", relation.name, got)
		}
	}
	if !domain.MatchesFeed(domain.FeedFriends, domain.DirectFriend, false) {
		t.Fatal("direct friend missing from friends feed")
	}
	if domain.MatchesFeed(domain.FeedFriendsOfFriends, domain.DirectFriend, false) {
		t.Fatal("direct friend leaked into exact-distance-two feed")
	}
	if !domain.MatchesFeed(domain.FeedFriendsOfFriends, domain.DistanceTwo, false) {
		t.Fatal("distance-two user missing")
	}
	if domain.MatchesFeed(domain.FeedFriends, domain.DistanceTwo, false) {
		t.Fatal("distance-two user leaked into friends feed")
	}
	if !domain.MatchesFeed(domain.FeedFollowing, domain.Unrelated, true) {
		t.Fatal("followed public account missing")
	}
}

func TestFeedVisibilityDoesNotGrantCommentPermission(t *testing.T) {
	t.Parallel()
	if !domain.VisibilityAllows(domain.VisibilityFriendsOfFriends, domain.DistanceTwo) {
		t.Fatal("distance two should see post")
	}
	if domain.CanComment(domain.VisibilityFriendsOfFriends, domain.DistanceTwo, false) {
		t.Fatal("distance two alone must not comment")
	}
	if !domain.CanComment(domain.VisibilityPublic, domain.Unrelated, false) {
		t.Fatal("public viewer should comment")
	}
	if !domain.CanComment(domain.VisibilityFriends, domain.DirectFriend, false) {
		t.Fatal("friend should comment")
	}
	if !domain.CanComment(domain.VisibilityFriends, domain.Unrelated, true) {
		t.Fatal("author should comment")
	}
}

func TestLocationTaggedUnionValidation(t *testing.T) {
	t.Parallel()
	valid := []domain.Location{
		domain.NoLocation(),
		mustExactLocation(t, 35.6812, 139.7671),
		mustApproximateLocation(t, "Tokyo"),
	}
	for _, location := range valid {
		if err := location.Validate(); err != nil {
			t.Errorf("valid location %#v: %v", location, err)
		}
	}
	if _, err := domain.ExactLocation(91, 0); err == nil {
		t.Fatal("invalid latitude accepted")
	}
	if _, err := domain.ExactLocation(0, 181); err == nil {
		t.Fatal("invalid longitude accepted")
	}
	for _, coordinate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := domain.ExactLocation(coordinate, 0); err == nil {
			t.Fatalf("non-finite latitude accepted: %v", coordinate)
		}
		if _, err := domain.ExactLocation(0, coordinate); err == nil {
			t.Fatalf("non-finite longitude accepted: %v", coordinate)
		}
	}
	if _, err := domain.ApproximateLocation(""); err == nil {
		t.Fatal("empty locality accepted")
	}
	if _, err := domain.ApproximateLocation(strings.Repeat("地", 101)); err == nil {
		t.Fatal("101-code-point locality accepted")
	}
}

func mustExactLocation(t *testing.T, latitude, longitude float64) domain.Location {
	t.Helper()
	location, err := domain.ExactLocation(latitude, longitude)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func mustApproximateLocation(t *testing.T, locality string) domain.Location {
	t.Helper()
	location, err := domain.ApproximateLocation(locality)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
