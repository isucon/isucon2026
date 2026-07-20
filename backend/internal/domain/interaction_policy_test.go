package domain_test

import (
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"testing"
)

func TestInteractionPolicies(t *testing.T) {
	for _, value := range []domain.ReactionType{domain.ReactionLike, domain.ReactionSmile, domain.ReactionSurprise, domain.ReactionHeart, domain.ReactionLaugh, domain.ReactionInstant} {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := domain.ReactionType("other").Validate(); err == nil {
		t.Fatal("unknown reaction accepted")
	}
	if domain.CanCreateRealMoji(uuid.Nil, uuid.Nil) {
		t.Fatal("self RealMoji accepted")
	}
	if !domain.ValidateComment("コメント") || domain.ValidateComment("") {
		t.Fatal("comment boundary broken")
	}
}
