package scenario

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
)

type notificationCloseTransport struct {
	http.RoundTripper
	closing chan struct{}
	release <-chan struct{}
}

func (t notificationCloseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		res.Body = &notificationCloseBody{ReadCloser: res.Body, closing: t.closing, release: t.release}
	}
	return res, err
}

type notificationCloseBody struct {
	io.ReadCloser
	closing chan struct{}
	release <-chan struct{}
}

func (b *notificationCloseBody) Close() error {
	close(b.closing)
	<-b.release
	return b.ReadCloser.Close()
}

func TestRunUserWaitsForNotificationClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	}))
	defer server.Close()
	closing, release := make(chan struct{}), make(chan struct{})
	releaseClose := sync.OnceFunc(func() { close(release) })
	defer releaseClose()
	transport := client.NewTransport()
	defer transport.CloseIdleConnections()
	api := client.NewClient(client.Config{Base: server.URL, Transport: notificationCloseTransport{
		RoundTripper: transport, closing: closing, release: release,
	}})
	runner, err := NewRunner(Config{Users: 1, Dataset: tinyDataset})
	if err != nil {
		t.Fatal(err)
	}
	u := runner.newUser(&agent.Agent{API: api, Region: "asia", Token: "token"}, browser)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	subscribed, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runner.runUser(ctx, u, false, subscribed)
	}()
	select {
	case <-subscribed:
	case <-time.After(time.Second):
		t.Fatal("notification subscription did not start")
	}
	cancel()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("notification response was not closed on cancellation")
	}
	select {
	case <-done:
		t.Fatal("runUser returned before notification Close completed")
	case <-time.After(50 * time.Millisecond):
	}
	releaseClose()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runUser did not return after notification Close completed")
	}
}
