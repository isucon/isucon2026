package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/zeebo/xxh3"
)

// Router がダイジェストを返したときも、バイト列を返したときも SameImage で比べられる。
func TestImageDigest(t *testing.T) {
	image := bytes.Repeat([]byte("jpeg"), 50_000)
	digest := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seed, err := strconv.ParseUint(r.Header.Get("X-Benchmark-Request-Id"), 10, 64)
		if err != nil {
			t.Errorf("X-Benchmark-Request-Id %q: %v", r.Header.Get("X-Benchmark-Request-Id"), err)
		}
		if !digest {
			_, _ = w.Write(image)
			return
		}
		w.Header().Set("X-Body-Xxh3", fmt.Sprintf("%016x", xxh3.HashSeed(image, seed)))
		w.Header().Set("X-Body-Length", strconv.Itoa(len(image)))
	}))
	defer server.Close()
	api := client.New(server.URL)
	for _, digest = range []bool{true, false} {
		got, err := api.Image(t.Context(), "/api/images/x?exp=1&sig=s")
		if err != nil {
			t.Fatal(err)
		}
		if (len(got) == len(image)) == digest {
			t.Errorf("digest=%v: got %d bytes", digest, len(got))
		}
		if !client.SameImage(got, image) {
			t.Errorf("digest=%v: the image does not match", digest)
		}
		other := bytes.Clone(image)
		other[len(other)-1]++
		if client.SameImage(got, other) || client.SameImage(got, image[1:]) {
			t.Errorf("digest=%v: another image matches", digest)
		}
	}
}

// InitialImage は Router に SHA-256 を求める。SHA-256 を返す Router、XXH3 しか返さない古い
// Router、ダイジェストを返さないとき（isureal-digest が止まっている）のどれでも SameImage で比べられ、
// XXH3 のとき以外は ImageSHA256 で中身の SHA-256 がわかる。
func TestInitialImageSHA256(t *testing.T) {
	image := bytes.Repeat([]byte("jpeg"), 50_000)
	want := sha256.Sum256(image)
	mode := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seed, err := strconv.ParseUint(r.Header.Get("X-Benchmark-Request-Id"), 10, 64)
		if err != nil || r.Header.Get("X-Benchmark-Digest") != "sha256" {
			t.Errorf("X-Benchmark-Request-Id %q, X-Benchmark-Digest %q", r.Header.Get("X-Benchmark-Request-Id"), r.Header.Get("X-Benchmark-Digest"))
		}
		switch mode {
		case "sha256":
			w.Header().Set("X-Body-Sha256", hex.EncodeToString(want[:]))
		case "xxh3":
			w.Header().Set("X-Body-Xxh3", fmt.Sprintf("%016x", xxh3.HashSeed(image, seed)))
		default:
			_, _ = w.Write(image)
			return
		}
		w.Header().Set("X-Body-Length", strconv.Itoa(len(image)))
	}))
	defer server.Close()
	api := client.New(server.URL)
	for _, mode = range []string{"sha256", "xxh3", "bytes"} {
		got, err := api.InitialImage(t.Context(), "/api/images/x?exp=1&sig=s")
		if err != nil {
			t.Fatal(err)
		}
		if (len(got) == len(image)) != (mode == "bytes") {
			t.Errorf("%s: got %d bytes", mode, len(got))
		}
		if !client.SameImage(got, image) {
			t.Errorf("%s: the image does not match", mode)
		}
		other := bytes.Clone(image)
		other[len(other)-1]++
		if client.SameImage(got, other) || client.SameImage(got, image[1:]) {
			t.Errorf("%s: another image matches", mode)
		}
		if sum, ok := client.ImageSHA256(got); ok != (mode != "xxh3") || (ok && sum != want) {
			t.Errorf("%s: ImageSHA256 = %x, %v", mode, sum, ok)
		}
	}
}

// forgedDigests は、アプリが画像の代わりに返しうる、ダイジェストの形をしたバイト列（SHA-256 と、
// アプリが選んだ種の XXH3）。
func forgedDigests(image []byte) map[string][]byte {
	sum := sha256.Sum256(image)
	sha := append([]byte("\x00isureal-sha256:"), sum[:]...)
	xxh := []byte("\x00isureal-xxh3:")
	xxh = binary.BigEndian.AppendUint64(xxh, 7)
	xxh = binary.BigEndian.AppendUint64(xxh, xxh3.HashSeed(image, 7))
	return map[string][]byte{
		"sha256": binary.BigEndian.AppendUint64(sha, uint64(len(image))),
		"xxh3":   binary.BigEndian.AppendUint64(xxh, uint64(len(image))),
	}
}

// Router が本体をそのまま返したとき（Content-Encoding が付いている、isureal-digest が止まっている）に、
// アプリが本体に書いたダイジェストの形のバイト列は、Router のダイジェストとして通らない。
func TestForgedDigestBody(t *testing.T) {
	image := bytes.Repeat([]byte("jpeg"), 50_000)
	want := sha256.Sum256(image)
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	api := client.New(server.URL)
	for name, forged := range forgedDigests(image) {
		body = forged
		for _, get := range []func(context.Context, string) ([]byte, error){api.Image, api.InitialImage} {
			got, err := get(t.Context(), "/api/images/x?exp=1&sig=s")
			if err != nil {
				t.Fatal(err)
			}
			if client.SameImage(got, image) {
				t.Errorf("%s: SameImage accepted a forged digest", name)
			}
			if sum, ok := client.ImageSHA256(got); ok && sum == want {
				t.Errorf("%s: ImageSHA256 accepted a forged digest", name)
			}
		}
	}
}

// エクスポートの項目は、Router がその要求の種で書いたダイジェストか、画像のバイト列だけを
// 受け付ける。503 の再試行でも種は変わらない。
func TestExportTrusted(t *testing.T) {
	image := bytes.Repeat([]byte("jpeg"), 50_000)
	var seeds []uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seed, err := strconv.ParseUint(r.Header.Get("X-Benchmark-Request-Id"), 10, 64)
		if err != nil {
			t.Errorf("X-Benchmark-Request-Id %q: %v", r.Header.Get("X-Benchmark-Request-Id"), err)
		}
		seeds = append(seeds, seed)
		if len(seeds) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("PK"))
	}))
	defer server.Close()
	c := client.NewClient(client.Config{Base: server.URL, Retry: client.RetryPolicy{MaxRetries: 1, Base: time.Millisecond, MaxWait: time.Millisecond}})
	export, err := c.Export(t.Context(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 2 || seeds[0] != seeds[1] {
		t.Fatalf("seeds %v, want the same seed twice", seeds)
	}
	router := []byte("\x00isureal-xxh3:")
	router = binary.BigEndian.AppendUint64(router, seeds[0])
	router = binary.BigEndian.AppendUint64(router, xxh3.HashSeed(image, seeds[0]))
	router = binary.BigEndian.AppendUint64(router, uint64(len(image)))
	if !export.Trusted(router) || !client.SameImage(router, image) {
		t.Errorf("the Router's digest is not accepted")
	}
	if !export.Trusted(image) {
		t.Errorf("the image bytes are not accepted")
	}
	for name, forged := range forgedDigests(image) {
		if export.Trusted(forged) {
			t.Errorf("%s: a forged digest is accepted", name)
		}
	}
}

func TestRegisterThenLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected legacy headers")
		}
		if r.Header.Get("X-Country-Code") != "DE" {
			t.Error("country header missing")
		}
		if r.Host != "app.127.0.0.1.nip.io" {
			t.Errorf("host=%s", r.Host)
		}
		switch r.URL.Path {
		case "/api/auth/register":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"secretKey":"secret","user":{"id":"u1","username":"alice"}}`)
		case "/api/auth/login":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["secretKey"] != "secret" {
				t.Error("wrong secret")
			}
			fmt.Fprint(w, `{"token":"token","user":{"id":"u1","username":"alice"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api := client.NewWithHost(server.URL, "app.127.0.0.1.nip.io").WithCountry("DE")
	registered, err := api.Register(t.Context(), "alice", "Alice", "europe")
	if err != nil || registered.SecretKey != "secret" || registered.Token != "" {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	user, err := api.Login(t.Context(), registered.Username, registered.SecretKey)
	if err != nil || user.Token != "token" || user.ID != registered.ID {
		t.Fatalf("user=%+v err=%v", user, err)
	}
}

func TestErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"username_taken"}`)
	}))
	defer server.Close()
	_, err := client.New(server.URL).Register(t.Context(), "alice", "Alice", "asia")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "username_taken" || apiErr.Path != "/api/auth/register" || apiErr.Want != 201 || apiErr.Status != 409 {
		t.Fatalf("err=%v", err)
	}
}

func TestPostMultipart(t *testing.T) {
	image := []byte{0xff, 0xd8, 0xff, 0xd9}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("location_kind") != "none" || len(r.MultipartForm.File["location"]) != 0 {
			t.Error("location must be regular form fields")
		}
		file, header, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, image) || header.Header.Get("Content-Type") != "image/jpeg" {
			t.Error("image changed")
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"p1","imageUrl":"/api/images/i1"}`)
	}))
	defer server.Close()
	post, err := client.New(server.URL).CreatePost(t.Context(), "token", image, "caption", "public", client.Location{Kind: "none"})
	if err != nil || post.ID != "p1" {
		t.Fatalf("post=%+v err=%v", post, err)
	}
}

func TestPaginationAndOpen(t *testing.T) {
	before := client.Post{ID: "p1", PostedAt: time.Date(2026, 9, 30, 1, 2, 3, 123, time.UTC)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/posting-window" {
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["region"] != "asia" || input["localDate"] != "2026-10-01" || r.Method != http.MethodPost {
				t.Errorf("open=%v", input)
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"w1","region":"asia","localDate":"2026-10-01"}`)
			return
		}
		if r.URL.Query().Get("beforeId") != before.ID || r.URL.Query().Get("beforePostedAt") != before.PostedAt.Format(time.RFC3339Nano) || r.URL.Query().Has("cursor") {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"items":[],"hasMore":true}`)
	}))
	defer server.Close()
	api := client.New(server.URL)
	feedBefore := before.FeedPost()
	feed, err := api.Feed(t.Context(), "token", "public", &feedBefore)
	if err != nil || !feed.HasMore {
		t.Fatalf("feed=%+v err=%v", feed, err)
	}
	memory, err := api.MemoriesMonth(t.Context(), "token", "2026-09", &before)
	if err != nil || !memory.HasMore {
		t.Fatalf("memory=%+v err=%v", memory, err)
	}
	window, err := api.OpenWindow(t.Context(), "admin", "asia", "2026-10-01")
	if err != nil || window.ID != "w1" {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestNotificationSubscriptionEstablishedBeforeEvent(t *testing.T) {
	send := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("stream headers")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-send:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"n1\",\"postingWindowId\":\"w1\",\"region\":\"asia\"}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := client.New(server.URL).OpenNotifications(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	close(send)
	notification, err := stream.Receive()
	if err != nil || notification.ID != "n1" || notification.PostingWindowID != "w1" {
		t.Fatalf("notification=%+v err=%v", notification, err)
	}
}

func TestNotificationCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.New(server.URL).OpenNotifications(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	cancel()
	if _, err := stream.Receive(); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

type recorder struct {
	mu      sync.Mutex
	records []client.Record
}

func (r *recorder) Observe(record client.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
}

func (r *recorder) all() []client.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]client.Record(nil), r.records...)
}

func fastRetry(observer client.Observer, base string) *client.Client {
	return client.NewClient(client.Config{
		Base:     base,
		Retry:    client.RetryPolicy{MaxRetries: 2, Base: time.Millisecond, MaxWait: 20 * time.Millisecond},
		Observer: observer,
	}).WithCountry("JP")
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var followed atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/me", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := client.New(server.URL).Me(t.Context(), "token")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err=%v", err)
	}
	if followed.Load() {
		t.Fatal("redirect target was requested")
	}
}

func TestRetry503ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"body":"hi"}` {
			t.Errorf("retried body=%q", body)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"c1","body":"hi"}`))
	}))
	defer server.Close()
	observed := &recorder{}
	if _, err := fastRetry(observed, server.URL).CreateComment(t.Context(), "token", "p1", "hi"); err != nil {
		t.Fatal(err)
	}
	records := observed.all()
	if len(records) != 1 {
		t.Fatalf("records=%+v", records)
	}
	record := records[0]
	if record.Route != "POST /api/posts/:postId/comments" || record.Attempts != 3 || !record.Saw503 || record.Err != nil || record.Status != 201 || record.Country != "JP" {
		t.Fatalf("record=%+v", record)
	}
}

func TestRetry503Exhausted(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	observed := &recorder{}
	_, err := fastRetry(observed, server.URL).Me(t.Context(), "token")
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 || apiErr.Attempts != 3 || calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if records := observed.all(); len(records) != 1 || records[0].Err == nil || !records[0].Saw503 {
		t.Fatalf("records=%+v", records)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"id":"u1"}`)
	}))
	defer server.Close()
	start := time.Now()
	if _, err := fastRetry(nil, server.URL).Me(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Retry-After was not capped: %v", elapsed)
	}
}

func TestOtherStatusesAreNotRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, err := fastRetry(nil, server.URL).Me(t.Context(), "token"); err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestCanceledOperationIsNotObserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	observed := &recorder{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := fastRetry(observed, server.URL).Me(ctx, "token"); err == nil {
		t.Fatal("expected error")
	}
	if records := observed.all(); len(records) != 0 {
		t.Fatalf("records=%+v", records)
	}
}

func TestImageUsesSignedURLOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("image request must not send Authorization")
		}
		if r.URL.Query().Get("sig") != "s" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		fmt.Fprint(w, "jpeg")
	}))
	defer server.Close()
	observed := &recorder{}
	data, err := fastRetry(observed, server.URL).Image(t.Context(), "/api/images/i1?exp=1&sig=s")
	if err != nil || string(data) != "jpeg" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if records := observed.all(); len(records) != 1 || records[0].Route != "GET /api/images/:imageId" {
		t.Fatalf("records=%+v", records)
	}
}

func TestOwnConnections(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"u1"}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	base := client.New(server.URL)
	alice := base.WithCountry("JP").WithOwnConnections()
	bob := base.WithCountry("DE").WithOwnConnections()
	for range 3 {
		for _, api := range []*client.Client{alice, bob} {
			if _, err := api.Me(t.Context(), "token"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 各ユーザーは自分の keep-alive 接続を使い回し、他人の接続は使わない。
	if got := connections.Load(); got != 2 {
		t.Fatalf("connections=%d, want 2", got)
	}
}

func TestMayFailWith(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	observed := &recorder{}
	api := fastRetry(observed, server.URL)
	// 許したステータスでも、呼び出し側にはエラーとして返る。
	if _, err := api.CreateComment(client.MayFailWith(t.Context(), http.StatusForbidden), "token", "p1", "hi"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := api.CreateComment(t.Context(), "token", "p1", "hi"); err == nil {
		t.Fatal("expected error")
	}
	records := observed.all()
	if len(records) != 2 || !records[0].Allowed || records[1].Allowed {
		t.Fatalf("records=%+v", records)
	}
}

// TestFeedDecodesOnlyUsedFields は、フィードの投稿をアプリの応答から必要な項目だけ読み、
// 投稿の応答から絞ったものと一致することを確かめる。
func TestFeedDecodesOnlyUsedFields(t *testing.T) {
	image := "/api/images/i1?exp=1&sig=s"
	locality := "Tokyo"
	post := client.Post{
		ID: "p1", PostingWindowID: "w1", ImageURL: image, Caption: "c", Visibility: "friends",
		Author:   client.UserSummary{ID: "u1", Username: "alice", DisplayName: "Alice", ProfileImageURL: &image, Bio: "hi", AccountType: "standard"},
		Location: client.Location{Kind: "locality", Locality: &locality},
		PostedAt: time.Date(2026, 10, 1, 9, 0, 0, 123000, time.UTC), OnTime: true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []client.Post{post}, "hasMore": true})
	}))
	defer server.Close()
	page, err := client.New(server.URL).Feed(t.Context(), "token", "friends", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("page=%+v", page)
	}
	if got, want := page.Items[0], post.FeedPost(); got.ID != want.ID || got.Author != want.Author || got.PostingWindowID != want.PostingWindowID ||
		got.ImageURL != want.ImageURL || got.Caption != want.Caption || got.Visibility != want.Visibility || !got.PostedAt.Equal(want.PostedAt) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestNotificationStreamReusesConnection は、通知を 1 件受けて閉じたストリームの接続を、
// 次の購読で使い回すことを確かめる（アプリは通知を 1 件送ると応答を終える）。
func TestNotificationStreamReusesConnection(t *testing.T) {
	var conns atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"id\":\"n1\",\"postingWindowId\":\"w1\",\"region\":\"asia\"}\n\n")
		// アプリと同じく通知を送り出してから応答を終える。終端は通知より後に届く。
		w.(http.Flusher).Flush()
		time.Sleep(10 * time.Millisecond)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	api := client.New(server.URL).WithOwnConnections()
	for range 3 {
		stream, err := api.OpenNotifications(t.Context(), "token")
		if err != nil {
			t.Fatal(err)
		}
		n, err := stream.Receive()
		if err != nil || n.PostingWindowID != "w1" {
			t.Fatalf("notification=%+v err=%v", n, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := conns.Load(); got != 1 {
		t.Fatalf("opened %d connections, want 1", got)
	}
}

// TestNotificationStreamCloseGivesUp は、通知のあとも応答を終えない実装でも、Close が
// 読み切るのを待ち続けないことを確かめる。
func TestNotificationStreamCloseGivesUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"n1\",\"postingWindowId\":\"w1\",\"region\":\"asia\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	stream, err := client.New(server.URL).OpenNotifications(t.Context(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Receive(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = stream.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
}

// TestConcurrentRequestsReuseConnections は、並行に取った画像の接続を残しておき、次に並行に
// 取るときに使い回すことを確かめる。
func TestConcurrentRequestsReuseConnections(t *testing.T) {
	const parallel = 6
	var conns atomic.Int64
	var arrived sync.WaitGroup
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// すべてのリクエストがそろうまで返さず、確実に並行に接続させる。
		arrived.Done()
		arrived.Wait()
		fmt.Fprint(w, "image")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	api := client.New(server.URL).WithOwnConnections()
	for range 2 {
		arrived.Add(parallel)
		var wg sync.WaitGroup
		for range parallel {
			wg.Go(func() {
				if _, err := api.Image(t.Context(), "/api/images/i1?exp=1&sig=s"); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	if got := conns.Load(); got != parallel {
		t.Fatalf("opened %d connections, want %d", got, parallel)
	}
}

// TestClientIPAndCode は、X-Client-IP を送り、期待外のステータスの応答の error を記録に残す
// ことを確かめる。
func TestClientIPAndCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Client-IP"); got != "203.0.113.7" {
			t.Errorf("X-Client-IP=%q", got)
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"ip_banned"}`))
	}))
	defer server.Close()
	observed := &recorder{}
	api := fastRetry(observed, server.URL).WithClientIP("203.0.113.7")
	if _, err := api.Me(t.Context(), "token"); err == nil {
		t.Fatal("expected error")
	}
	records := observed.all()
	if len(records) != 1 || records[0].Code != "ip_banned" || records[0].Status != http.StatusForbidden {
		t.Fatalf("records=%+v", records)
	}
}

// TestTruncatedBodyIsRequestError は、応答の本体を最後まで読めなかったときに、応答を
// 受け取れなかったのと同じ RequestError になることを確かめる（整合性の違反にしない）。
func TestTruncatedBodyIsRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				conn.Close()
			}
		}
	}))
	t.Cleanup(server.Close)
	c := client.NewClient(client.Config{Base: server.URL, Retry: client.RetryPolicy{}})
	_, err := c.Export(context.Background(), "token")
	var requestErr *client.RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("err=%v (%T), want RequestError", err, err)
	}
}

// TestOversizedBody は、上限（JSON は 1 MiB）を超える応答を切り詰めて使わず、エラーにする
// ことを確かめる。先頭の 1 MiB が正しい JSON と空白でも通さない。期待外のステータスの応答は
// 上限までで error を読む。
func TestOversizedBody(t *testing.T) {
	const limit = 1 << 20
	prefix := `{"id":"u1","username":"alice"}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"exactly the limit", http.StatusOK, prefix + strings.Repeat(" ", limit-len(prefix)), true},
		{"one byte over", http.StatusOK, prefix + strings.Repeat(" ", limit-len(prefix)+1), false},
		{"invalid trailer", http.StatusOK, prefix + strings.Repeat(" ", limit-len(prefix)) + "INVALID_TRAILER", false},
		{"error status", http.StatusForbidden, `{"error":"forbidden"}` + strings.Repeat(" ", limit), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			observed := &recorder{}
			user, err := fastRetry(observed, server.URL).GetUser(t.Context(), "token", "u1")
			if tc.ok {
				if err != nil || user.ID != "u1" {
					t.Fatalf("user=%+v err=%v", user, err)
				}
				return
			}
			var bodyErr *client.BodyError
			var apiErr *client.APIError
			switch {
			case tc.status == http.StatusOK && !errors.As(err, &bodyErr):
				t.Fatalf("err=%v (%T), want BodyError", err, err)
			case tc.status != http.StatusOK && (!errors.As(err, &apiErr) || apiErr.Code != "forbidden"):
				t.Fatalf("err=%v (%T), want APIError with the code", err, err)
			}
			if records := observed.all(); len(records) != 1 || records[0].Err == nil {
				t.Fatalf("records=%+v", records)
			}
		})
	}
}

// TestMayFailWithTruncatedBody は、許したステータスの応答でも本体を読み切れなかったときは、
// 許さずに通信の失敗として記録することを確かめる。
func TestMayFailWithTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("{"))
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				conn.Close()
			}
		}
	}))
	defer server.Close()
	observed := &recorder{}
	_, err := fastRetry(observed, server.URL).CreateComment(client.MayFailWith(t.Context(), http.StatusForbidden), "token", "p1", "hi")
	var requestErr *client.RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("err=%v (%T), want RequestError", err, err)
	}
	records := observed.all()
	if len(records) != 1 || records[0].Allowed || records[0].Err == nil {
		t.Fatalf("records=%+v", records)
	}
}
