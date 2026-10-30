package client_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestNotificationMediaType は、通知の Content-Type を media type として読み、type と subtype を
// 大文字と小文字を区別せずに text/event-stream と比べることを確かめる。
func TestNotificationMediaType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		allowed     bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"Text/Event-Stream; Charset=UTF-8", true},
		{"text/event-stream;charset", true},
		{"text/event-stream-invalid", false},
		{"text/event-streams", false},
		{"text/plain", false},
		{"text/event-stream/extra", false},
		{"", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				fmt.Fprint(w, "data: {\"id\":\"n1\",\"postingWindowId\":\"w1\",\"region\":\"asia\"}\n\n")
			}))
			defer server.Close()
			stream, err := client.New(server.URL).OpenNotifications(t.Context(), "token")
			if (err == nil) != tc.allowed {
				t.Fatalf("Content-Type %q: err=%v, want accepted=%v", tc.contentType, err, tc.allowed)
			}
			if stream == nil {
				return
			}
			defer stream.Close()
			if n, err := stream.Receive(); err != nil || n.ID != "n1" {
				t.Fatalf("notification=%+v err=%v", n, err)
			}
		})
	}
}
