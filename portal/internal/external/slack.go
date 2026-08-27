// Package external は Slack / Discord / GitHub / Web Push / AWS / SSH への接続を提供する。
// いずれも設定が無ければ no-op でログのみ残す（ローカル・テスト環境で外に出ないため）。
package external

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type Slack struct {
	WebhookURL string
}

// Post は SlackWebhookJob 相当（payload=<json> の form POST）。
func (s *Slack) Post(ctx context.Context, text string) error {
	if s.WebhookURL == "" {
		slog.Info("slack (skipped: no webhook url)", "text", text)
		return nil
	}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	form := url.Values{"payload": {string(payload)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}
