package service

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
)

var (
	ErrPostNotVisible = errors.New("post is not visible")
	ErrInvalidPost    = errors.New("invalid post")
)

type PostImageStore interface {
	Save(context.Context, uuid.UUID, isurealimage.Purpose, io.Reader) (isurealimage.Object, error)
	Remove(string) error
}

type PostRepository interface {
	CurrentWindow(context.Context, uuid.UUID) (domain.PostingWindow, domain.PostSlot, error)
	Create(context.Context, uuid.UUID, isurealimage.Object, string, domain.Visibility, domain.Location, time.Time, uuid.UUID) (domain.Post, error)
	Get(context.Context, uuid.UUID) (domain.PostRecord, error)
	ImagePost(context.Context, uuid.UUID) (uuid.UUID, string, error)
	CurrentSelf(context.Context, uuid.UUID) (domain.PostRecord, error)
	Update(context.Context, uuid.UUID, uuid.UUID, *string, *domain.Visibility) (domain.PostRecord, error)
	Delete(context.Context, uuid.UUID, uuid.UUID, time.Time) (string, error)
	ListFeed(context.Context, domain.FeedQuery) ([]domain.PostRecord, error)
}

type PostService struct {
	repository PostRepository
	visibility *VisibilityPolicy
	images     PostImageStore
	clock      interface{ Now() time.Time }
	ids        domain.IDGenerator
}

type CreatePostInput struct {
	Image      io.Reader
	Caption    string
	Visibility domain.Visibility
	Location   domain.Location
}

type UpdatePostInput struct {
	Caption    *string
	Visibility *domain.Visibility
}

func NewPostService(repository PostRepository, visibility *VisibilityPolicy, images PostImageStore, clock interface{ Now() time.Time }, ids domain.IDGenerator) *PostService {
	return &PostService{repository: repository, visibility: visibility, images: images, clock: clock, ids: ids}
}

func (s *PostService) CurrentWindow(ctx context.Context, userID uuid.UUID) (domain.PostingWindow, domain.PostSlot, error) {
	return s.repository.CurrentWindow(ctx, userID)
}

func (s *PostService) Create(ctx context.Context, authorID uuid.UUID, input CreatePostInput) (domain.PostRecord, error) {
	if input.Image == nil || domain.ValidateCaption(input.Caption) != nil || input.Visibility.Validate() != nil || input.Location.Validate() != nil {
		return domain.PostRecord{}, ErrInvalidPost
	}
	object, err := s.images.Save(ctx, authorID, isurealimage.PostImage, input.Image)
	if err != nil {
		return domain.PostRecord{}, err
	}
	post, err := s.repository.Create(ctx, authorID, object, input.Caption, input.Visibility, input.Location, s.clock.Now(), s.ids.New())
	if err != nil {
		_ = s.images.Remove(object.ObjectKey)
		return domain.PostRecord{}, err
	}
	record, err := s.repository.Get(ctx, post.ID)
	if err != nil {
		return domain.PostRecord{}, err
	}
	return record, nil
}

func (s *PostService) Get(ctx context.Context, viewerID, postID uuid.UUID) (domain.PostRecord, error) {
	allowed, err := s.visibility.CanViewPost(ctx, viewerID, postID)
	if err != nil {
		return domain.PostRecord{}, err
	}
	if !allowed {
		return domain.PostRecord{}, ErrPostNotVisible
	}
	return s.repository.Get(ctx, postID)
}

func (s *PostService) CurrentSelf(ctx context.Context, userID uuid.UUID) (domain.PostRecord, error) {
	return s.repository.CurrentSelf(ctx, userID)
}

func (s *PostService) ResolveAuthorizedImage(ctx context.Context, viewer domain.User, imageID uuid.UUID) (string, error) {
	postID, objectKey, err := s.repository.ImagePost(ctx, imageID)
	if err != nil {
		return "", err
	}
	allowed, err := s.visibility.CanViewPost(ctx, viewer.ID, postID)
	if err != nil || !allowed {
		return "", ErrPostNotVisible
	}
	return objectKey, nil
}

func (s *PostService) Update(ctx context.Context, actorID, postID uuid.UUID, input UpdatePostInput) (domain.PostRecord, error) {
	if input.Caption == nil && input.Visibility == nil {
		return domain.PostRecord{}, ErrInvalidPost
	}
	if input.Caption != nil && domain.ValidateCaption(*input.Caption) != nil {
		return domain.PostRecord{}, ErrInvalidPost
	}
	if input.Visibility != nil && input.Visibility.Validate() != nil {
		return domain.PostRecord{}, ErrInvalidPost
	}
	return s.repository.Update(ctx, actorID, postID, input.Caption, input.Visibility)
}

func (s *PostService) Delete(ctx context.Context, actorID, postID uuid.UUID) error {
	objectKey, err := s.repository.Delete(ctx, actorID, postID, s.clock.Now())
	if err != nil {
		return err
	}
	return s.images.Remove(objectKey)
}

func (s *PostService) Feed(ctx context.Context, viewerID uuid.UUID, kind domain.FeedKind, beforePostedAt *time.Time, beforeID *uuid.UUID, limit int) ([]domain.PostRecord, error) {
	if kind != domain.FeedFriends && kind != domain.FeedFriendsOfFriends && kind != domain.FeedPublic && kind != domain.FeedFollowing {
		return nil, ErrInvalidPost
	}
	return s.repository.ListFeed(ctx, domain.FeedQuery{ViewerID: viewerID, Kind: kind, BeforePostedAt: beforePostedAt, BeforeID: beforeID, Limit: limit})
}
