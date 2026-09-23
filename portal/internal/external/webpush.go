package external

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// WebPush は VAPID 鍵による Web Push 配信（DeliverPushNotificationJob 相当）。
// 鍵は既存形式（PEM の EC 秘密鍵、CT-10）を読む。
type WebPush struct {
	Subject string

	privateKey string // base64url(raw d)
	publicKey  string // base64url(uncompressed point)
	enabled    bool
}

// NewWebPush は PEM（空なら無効）から VAPID 鍵を読み込む。
func NewWebPush(pemStr, subject string) (*WebPush, error) {
	w := &WebPush{Subject: subject}
	if pemStr == "" {
		return w, nil
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("webpush: invalid PEM")
	}
	key, err := parseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("webpush: %w", err)
	}
	w.privateKey = base64.RawURLEncoding.EncodeToString(key.D.Bytes())
	w.publicKey = base64.RawURLEncoding.EncodeToString(
		elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y))
	w.enabled = true
	return w, nil
}

func parseECPrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an EC private key")
	}
	return ec, nil
}

// PublicKeyForPushHeader は WebPush::VapidKey#public_key_for_push_header 相当
// （GetCurrentSessionResponse.push_vapid_key に載る）。
func (w *WebPush) PublicKeyForPushHeader() string {
	return w.publicKey
}

// ErrSubscriptionGone は購読の失効（410/404）。呼び出し側が購読を削除する。
var ErrSubscriptionGone = errors.New("webpush: subscription gone")

// Send は base64(pb) のペイロード（CT-4）をそのまま送る。
func (w *WebPush) Send(ctx context.Context, endpoint, p256dh, auth, payload string) error {
	if !w.enabled {
		slog.Info("webpush (skipped: no vapid key)", "endpoint", endpoint)
		return nil
	}
	sub := &webpush.Subscription{
		Endpoint: endpoint,
		Keys:     webpush.Keys{P256dh: p256dh, Auth: auth},
	}
	res, err := webpush.SendNotificationWithContext(ctx, []byte(payload), sub, &webpush.Options{
		Subscriber:      w.Subject,
		VAPIDPublicKey:  w.publicKey,
		VAPIDPrivateKey: w.privateKey,
		TTL:             60,
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusGone || res.StatusCode == http.StatusNotFound {
		return ErrSubscriptionGone
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("webpush: %d", res.StatusCode)
	}
	return nil
}
