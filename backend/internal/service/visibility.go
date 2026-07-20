package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

type VisibilityFacts = domain.VisibilityFacts

type VisibilityFactsProvider interface {
	VisibilityFacts(context.Context, uuid.UUID, uuid.UUID) (VisibilityFacts, error)
}

type VisibilityPolicy struct{ provider VisibilityFactsProvider }

func NewVisibilityPolicy(provider VisibilityFactsProvider) *VisibilityPolicy {
	return &VisibilityPolicy{provider: provider}
}

func (p *VisibilityPolicy) Facts(ctx context.Context, viewerID, postID uuid.UUID) (VisibilityFacts, error) {
	return p.provider.VisibilityFacts(ctx, viewerID, postID)
}

func (p *VisibilityPolicy) CanViewPost(ctx context.Context, viewerID, postID uuid.UUID) (bool, error) {
	facts, err := p.Facts(ctx, viewerID, postID)
	if err != nil {
		return false, err
	}
	if facts.ViewerID == facts.AuthorID {
		return facts.AuthorActive, nil
	}
	return commonVisibility(facts) && domain.VisibilityAllows(facts.Visibility, facts.Relationship), nil
}

func (p *VisibilityPolicy) CanListInFeed(ctx context.Context, viewerID, postID uuid.UUID, feed domain.FeedKind) (bool, error) {
	facts, err := p.Facts(ctx, viewerID, postID)
	if err != nil {
		return false, err
	}
	if facts.ViewerID == facts.AuthorID || !commonVisibility(facts) {
		return false, nil
	}
	return domain.VisibilityAllows(facts.Visibility, facts.Relationship) && domain.MatchesFeed(feed, facts.Relationship, facts.Followed), nil
}

func (p *VisibilityPolicy) CanAddRealMoji(ctx context.Context, viewerID, postID uuid.UUID) (bool, error) {
	facts, err := p.Facts(ctx, viewerID, postID)
	if err != nil {
		return false, err
	}
	return facts.ViewerID != facts.AuthorID && commonVisibility(facts) && domain.VisibilityAllows(facts.Visibility, facts.Relationship), nil
}

func (p *VisibilityPolicy) CanComment(ctx context.Context, viewerID, postID uuid.UUID) (bool, error) {
	facts, err := p.Facts(ctx, viewerID, postID)
	if err != nil {
		return false, err
	}
	if !facts.ViewerActive || !facts.AuthorActive || facts.Blocked || !facts.PostCurrent || !facts.ViewerPosted {
		return false, nil
	}
	return domain.CanComment(facts.Visibility, facts.Relationship, facts.ViewerID == facts.AuthorID), nil
}

func commonVisibility(facts VisibilityFacts) bool {
	return facts.ViewerActive && facts.AuthorActive && !facts.Blocked && facts.ViewerPosted && facts.PostCurrent
}
