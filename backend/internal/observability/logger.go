package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"
)

var forbiddenKeys = []string{
	"password", "secret", "token", "authorization", "cookie", "idempotency", "image", "caption", "comment",
	"body", "bio", "latitude", "longitude", "endpoint", "p256dh", "auth_secret", "vapid_private",
}

func New(output io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(&redactingHandler{next: slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})})
}

type redactingHandler struct{ next slog.Handler }

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.next.Handle(ctx, redactRecord(record))
}
func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &redactingHandler{next: h.next.WithAttrs(redactAttrs(attrs))}
}
func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactRecord(record slog.Record) slog.Record {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool { clean.AddAttrs(redactAttr(attr)); return true })
	return clean
}

func redactAttrs(attrs []slog.Attr) []slog.Attr {
	clean := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		clean[index] = redactAttr(attr)
	}
	return clean
}

func redactAttr(attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)
	for _, forbidden := range forbiddenKeys {
		if strings.Contains(key, forbidden) {
			return slog.String(attr.Key, "[REDACTED]")
		}
	}
	if attr.Value.Kind() == slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redactAttrs(attr.Value.Group())...)}
	}
	return attr
}

type Event struct {
	Timestamp     time.Time `json:"timestamp"`
	Operation     string    `json:"operation"`
	Result        string    `json:"result"`
	CorrelationID string    `json:"correlationId"`
}

func (event Event) Valid() bool {
	_, err := json.Marshal(event)
	return err == nil && !event.Timestamp.IsZero() && event.Operation != "" && event.Result != "" && event.CorrelationID != ""
}
