package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// sseServer は chunks を 1 つずつ送り出す（間を空けて、別々に届くようにする）通知のストリーム。
// hold なら送り終えても応答を終えず、接続を開いたままにする。
func sseServer(t *testing.T, chunks []string, hold bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
		if hold {
			<-r.Context().Done()
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestNotificationSSEFraming は、SSE で認められた行の区切り（LF、CRLF、CR）と先頭の BOM の
// どれでも通知を受け取れることを確かめる。区切りが読み取りの切れ目にまたがっても、CR のあとに
// 何も届かなくても、空行で確定したイベントを返す。
func TestNotificationSSEFraming(t *testing.T) {
	const event = `{"id":"n1","postingWindowId":"w1","region":"asia"}`
	for _, tc := range []struct {
		name   string
		chunks []string
	}{
		{"LF", []string{"data: " + event + "\n\n"}},
		{"CRLF", []string{"data: " + event + "\r\n\r\n"}},
		{"CR", []string{"data: " + event + "\r\r"}},
		{"BOM LF", []string{"\ufeffdata: " + event + "\n\n"}},
		{"BOM CR", []string{"\ufeffdata: " + event + "\r\r"}},
		{"CRLF split", []string{"data: " + event + "\r", "\n\r", "\n"}},
		{"CR at chunk end", []string{": comment\r", "data: " + event + "\r", "\r"}},
		{"multiline data", []string{"data: {\"id\":\"n1\",\r\ndata: \"postingWindowId\":\"w1\",\"region\":\"asia\"}\r\n\r\n"}},
		{"field without colon", []string{"event: notification\ndata\ndata: " + event[:10] + "\ndata:" + event[10:] + "\n\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 送り終えても応答を終えない実装でも、空行で確定したイベントを返す。
			for _, hold := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				stream, err := client.New(sseServer(t, tc.chunks, hold).URL).OpenNotifications(ctx, "token")
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				var got client.Notification
				go func() {
					defer close(done)
					got, err = stream.Receive()
				}()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatalf("hold=%v: Receive did not return", hold)
				}
				// 応答を終えないストリームの残りを読み切るのを待たずに閉じる。
				cancel()
				_ = stream.Close()
				if err != nil || got.ID != "n1" || got.PostingWindowID != "w1" || got.Region != "asia" {
					t.Fatalf("hold=%v: notification=%+v err=%v", hold, got, err)
				}
			}
		})
	}
}

// TestNotificationSSEEvents は、1 つのストリームの複数のイベントを順に読み、空行の前に終わった
// イベントは捨てて io.EOF を返すことを確かめる。
func TestNotificationSSEEvents(t *testing.T) {
	server := sseServer(t, []string{
		"data: {\"id\":\"n1\"}\r\r",
		"\r\n",
		"data: {\"id\":\"n2\"}\n\n",
		"data: {\"id\":\"n3\"}\n",
	}, false)
	stream, err := client.New(server.URL).OpenNotifications(t.Context(), "token")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, want := range []string{"n1", "n2"} {
		if n, err := stream.Receive(); err != nil || n.ID != want {
			t.Fatalf("notification=%+v err=%v, want %s", n, err, want)
		}
	}
	if n, err := stream.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("notification=%+v err=%v, want EOF", n, err)
	}
}

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
