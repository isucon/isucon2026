package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/url"
)

var ErrInvalidPushSubscription = errors.New("invalid push subscription")

type PushSubscriptionStore interface {
	ReplacePush(context.Context, uuid.UUID, string, string, string, string) error
	RemovePush(context.Context, uuid.UUID, string) error
}
type PushSubscriptionService struct{ repository PushSubscriptionStore }

func NewPushSubscriptionService(repository PushSubscriptionStore) *PushSubscriptionService {
	return &PushSubscriptionService{repository: repository}
}
func (s *PushSubscriptionService) Replace(ctx context.Context, user uuid.UUID, token, endpoint, p256dh, auth string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(endpoint) > 2048 || p256dh == "" || auth == "" {
		return ErrInvalidPushSubscription
	}
	return s.repository.ReplacePush(ctx, user, token, endpoint, p256dh, auth)
}
func (s *PushSubscriptionService) Remove(ctx context.Context, user uuid.UUID, token string) error {
	return s.repository.RemovePush(ctx, user, token)
}
