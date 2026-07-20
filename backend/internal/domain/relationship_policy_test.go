package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

func TestRelationshipPolicies(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	pair, err := domain.NormalizePair(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Low.String() > pair.High.String() {
		t.Fatal("pair not normalized")
	}
	if _, err := domain.NormalizePair(a, a); err == nil {
		t.Fatal("self pair accepted")
	}
	request := domain.FriendRequest{RequesterID: a, RecipientID: b, Status: domain.RequestPending}
	if err := request.CanTransition(b, domain.RequestAccepted); err != nil {
		t.Fatal(err)
	}
	if err := request.CanTransition(a, domain.RequestAccepted); err == nil {
		t.Fatal("requester accepted own request")
	}
	if err := request.CanTransition(a, domain.RequestCancelled); err != nil {
		t.Fatal(err)
	}
	if domain.CanFollow("regular") {
		t.Fatal("regular account followable")
	}
	if !domain.CanFollow("certified_public") {
		t.Fatal("certified account not followable")
	}
	general := domain.DiscloseProfile(domain.User{Bio: "secret", AccountType: "regular"}, false)
	if general.Bio != "" {
		t.Fatal("general bio disclosed")
	}
	certified := domain.DiscloseProfile(domain.User{Bio: "public", AccountType: "certified_public"}, false)
	if certified.Bio != "public" {
		t.Fatal("certified bio hidden")
	}
}
