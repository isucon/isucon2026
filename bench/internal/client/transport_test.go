package client_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// resetPeer は応答後も接続を維持し、クライアントの切断で Read が返したエラーを通知する。
// net/http のサーバーでは隠される EOF（FIN）と ECONNRESET（RST）の違いを確かめる。
func resetPeer(t *testing.T, response string) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			done <- err
			return
		}
		_, err = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		if err == nil {
			_, err = io.WriteString(conn, response)
		}
		if err == nil {
			_, err = reader.ReadByte()
		}
		done <- err
	}()
	return "http://" + listener.Addr().String(), done
}

func assertPeerReset(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("peer read: %v, want ECONNRESET (RST rather than FIN)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("peer did not observe the connection closing")
	}
}

func TestIdleConnectionClosesWithReset(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("ownConnections=%t", own), func(t *testing.T) {
			body := `{"language":"go"}`
			base, done := resetPeer(t, fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body))
			api := client.New(base)
			if own {
				api = api.WithOwnConnections()
			}
			t.Cleanup(api.CloseIdleConnections)
			if _, err := api.Initialize(t.Context()); err != nil {
				t.Fatal(err)
			}
			api.CloseIdleConnections()
			assertPeerReset(t, done)
		})
	}
}

func TestNotificationCancellationClosesWithReset(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("ownConnections=%t", own), func(t *testing.T) {
			base, done := resetPeer(t, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
			api := client.New(base)
			if own {
				api = api.WithOwnConnections()
			}
			t.Cleanup(api.CloseIdleConnections)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := api.OpenNotifications(ctx, "token")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			cancel()
			if _, err := stream.Receive(); !errors.Is(err, context.Canceled) {
				t.Fatalf("Receive: %v, want context.Canceled", err)
			}
			assertPeerReset(t, done)
		})
	}
}
