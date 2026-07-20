package push

import (
	"context"
	"encoding/json"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"io"
	"net/http"
)

type Result struct {
	Transient, Permanent bool
	ErrorClass           string
}
type Sender interface {
	Send(context.Context, domain.PushAttempt) Result
}
type WebPush struct{ PublicKey, PrivateKey, Subject string }

func (w WebPush) Send(ctx context.Context, item domain.PushAttempt) Result {
	payload, _ := json.Marshal(map[string]any{"notificationId": item.NotificationID})
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: item.Endpoint, Keys: webpush.Keys{P256dh: item.P256DH, Auth: item.Auth}}, &webpush.Options{Subscriber: w.Subject, VAPIDPublicKey: w.PublicKey, VAPIDPrivateKey: w.PrivateKey, TTL: 60})
	if err != nil {
		return Result{Transient: true, ErrorClass: "unknown_result"}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return Result{}
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return Result{Permanent: true, ErrorClass: "invalid_subscription"}
	default:
		return Result{Transient: true, ErrorClass: "transient_response"}
	}
}
