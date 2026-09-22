// Package client is the benchmarker's HTTP client for the ISUReal API.
//
// Contract rules it enforces (spec.md FR-044, spec-bench.md §4):
//   - authentication only via Authorization: Bearer — never cookies
//   - Idempotency-Key (UUID) on every state-changing request
//   - no Origin / Sec-Fetch-Site headers (headerless non-browser client)
//   - multipart file parts carry a filename (the app reads them via FormFile)
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

const (
	apiPrefix          = "/api/v1"
	requestTimeout     = 15 * time.Second
	maxErrorBodyBytes  = 1 << 20
	locationNoneJSON   = `{"kind":"none"}`
	multipartImageName = "photo.jpg"
)

type Client struct {
	base string
	host string
	http *http.Client
}

func New(base string) *Client {
	return NewWithHost(base, "")
}

// NewWithHost creates a client that connects to base while overriding the
// HTTP Host header. This is useful when a benchmark ingress is reached through
// an IP/port forward but routes requests by the public application hostname.
func NewWithHost(base, host string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		host: host,
		// No cookie jar: any Cookie header is rejected with 400 by the app.
		http: &http.Client{Timeout: requestTimeout},
	}
}

// NewUUID returns a random v4 UUID for Idempotency-Key headers.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// APIError reports an unexpected status with the diagnostic fields required
// by spec-bench.md DIAG-01 (method, path, expected and actual values).
type APIError struct {
	Method string
	Path   string
	Status int
	Want   int
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: expected %d, got %d (%s)", e.Method, e.Path, e.Want, e.Status, e.Code)
}

type request struct {
	method      string
	path        string
	token       string
	idempotent  bool
	contentType string
	body        []byte
	want        int
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	httpRequest, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, bytes.NewReader(r.body))
	if err != nil {
		return err
	}
	if c.host != "" {
		httpRequest.Host = c.host
	}
	if r.contentType != "" {
		httpRequest.Header.Set("Content-Type", r.contentType)
	}
	if r.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.token)
	}
	if r.idempotent {
		httpRequest.Header.Set("Idempotency-Key", NewUUID())
	}
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("%s %s: %w", r.method, r.path, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", r.method, r.path, err)
	}
	if response.StatusCode != r.want {
		return &APIError{Method: r.method, Path: r.path, Status: response.StatusCode, Want: r.want, Code: problemCode(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", r.method, r.path, err)
		}
	}
	return nil
}

func problemCode(body []byte) string {
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &problem); err != nil || problem.Code == "" {
		return "unknown"
	}
	return problem.Code
}

func marshalJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal request: %v", err))
	}
	return data
}

// --- competition operation endpoints ---

type InitializeResult struct {
	Language string    `json:"language"`
	BaseTime time.Time `json:"base_time"`
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	var result InitializeResult
	err := c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/initialize", want: http.StatusOK}, &result)
	return result, err
}

func (c *Client) AdvanceVirtualTime(ctx context.Context, offsetSeconds int64) (time.Time, error) {
	var result struct {
		CurrentTime time.Time `json:"current_time"`
	}
	err := c.do(ctx, request{
		method:      http.MethodPost,
		path:        apiPrefix + "/virtual-time/advance",
		contentType: "application/json",
		body:        marshalJSON(map[string]int64{"offset_seconds": offsetSeconds}),
		want:        http.StatusOK,
	}, &result)
	return result.CurrentTime, err
}

// --- auth ---

type User struct {
	ID       string
	Username string
	Token    string
}

type sessionResponse struct {
	Token string `json:"token"`
	User  struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

func (s sessionResponse) user() User {
	return User{ID: s.User.ID, Username: s.User.Username, Token: s.Token}
}

func (c *Client) Register(ctx context.Context, username, displayName, region string) (User, error) {
	body := marshalJSON(map[string]string{
		"username": username, "displayName": displayName, "region": region,
	})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/register",
		idempotent: true, contentType: "application/json", body: body, want: http.StatusCreated,
	}, &session)
	return session.user(), err
}

func (c *Client) Login(ctx context.Context, username, secretKey string) (User, error) {
	body := marshalJSON(map[string]string{"username": username, "secretKey": secretKey})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/login",
		idempotent: true, contentType: "application/json", body: body, want: http.StatusOK,
	}, &session)
	return session.user(), err
}

func (c *Client) AdminLogin(ctx context.Context, username, password string) (string, error) {
	body := marshalJSON(map[string]string{"username": username, "password": password})
	var result struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/admin/auth/login",
		idempotent: true, contentType: "application/json", body: body, want: http.StatusOK,
	}, &result)
	return result.Token, err
}

// --- admin schedule ---

func (c *Client) SetSchedule(ctx context.Context, adminToken, region string, at time.Time) error {
	body := marshalJSON(map[string]string{"scheduledAt": at.UTC().Format(time.RFC3339)})
	return c.do(ctx, request{
		method: http.MethodPut, path: apiPrefix + "/admin/regions/" + region + "/schedule",
		token: adminToken, idempotent: true, contentType: "application/json", body: body, want: http.StatusOK,
	}, nil)
}

// --- posting ---

type Window struct {
	ID        string    `json:"id"`
	Region    string    `json:"region"`
	LocalDate string    `json:"localDate"`
	StartsAt  time.Time `json:"startsAt"`
}

type WindowStatus struct {
	Window    *Window `json:"window"`
	HasPosted bool    `json:"hasPosted"`
	CanPost   bool    `json:"canPost"`
}

func (c *Client) CurrentWindow(ctx context.Context, token string) (WindowStatus, error) {
	var status WindowStatus
	err := c.do(ctx, request{
		method: http.MethodGet, path: apiPrefix + "/posting-window/current", token: token, want: http.StatusOK,
	}, &status)
	return status, err
}

type Post struct {
	ID     string `json:"id"`
	Author struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"author"`
	ImageURL string    `json:"imageUrl"`
	PostedAt time.Time `json:"postedAt"`
	OnTime   bool      `json:"onTime"`
}

type filePart struct {
	field       string
	filename    string
	contentType string
	data        []byte
}

func buildMultipart(fields map[string]string, files []filePart) (string, []byte, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for field, value := range fields {
		if err := writer.WriteField(field, value); err != nil {
			return "", nil, err
		}
	}
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.field, file.filename))
		header.Set("Content-Type", file.contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return "", nil, err
		}
		if _, err := part.Write(file.data); err != nil {
			return "", nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	return writer.FormDataContentType(), buffer.Bytes(), nil
}

func (c *Client) CreatePost(ctx context.Context, token string, image []byte, caption, visibility string) (Post, error) {
	contentType, body, err := buildMultipart(
		map[string]string{"caption": caption, "visibility": visibility},
		[]filePart{
			{field: "image", filename: multipartImageName, contentType: "image/jpeg", data: image},
			{field: "location", filename: "location.json", contentType: "application/json", data: []byte(locationNoneJSON)},
		},
	)
	if err != nil {
		return Post{}, err
	}
	var post Post
	err = c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts",
		token: token, idempotent: true, contentType: contentType, body: body, want: http.StatusCreated,
	}, &post)
	return post, err
}

type FeedPage struct {
	Items      []Post  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func (c *Client) Feed(ctx context.Context, token, feed, cursor string) (FeedPage, error) {
	path := apiPrefix + "/feeds/" + feed
	if cursor != "" {
		path += "?cursor=" + cursor
	}
	var page FeedPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}

// --- interactions ---

func (c *Client) CreateComment(ctx context.Context, token, postID, body string) error {
	return c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts/" + postID + "/comments",
		token: token, idempotent: true, contentType: "application/json",
		body: marshalJSON(map[string]string{"body": body}), want: http.StatusCreated,
	}, nil)
}

func (c *Client) ReplaceRealMoji(ctx context.Context, token, postID, reactionType string, image []byte) error {
	contentType, body, err := buildMultipart(
		map[string]string{"reactionType": reactionType},
		[]filePart{{field: "image", filename: multipartImageName, contentType: "image/jpeg", data: image}},
	)
	if err != nil {
		return err
	}
	return c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts/" + postID + "/realmojis",
		token: token, idempotent: true, contentType: contentType, body: body, want: http.StatusCreated,
	}, nil)
}

// --- reads ---

type Notification struct {
	ID              string `json:"id"`
	PostingWindowID string `json:"postingWindowId"`
	Region          string `json:"region"`
	Read            bool   `json:"read"`
}

type NotificationPage struct {
	Items []Notification `json:"items"`
}

func (c *Client) Notifications(ctx context.Context, token string) (NotificationPage, error) {
	var page NotificationPage
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/notifications", token: token, want: http.StatusOK}, &page)
	return page, err
}

type Profile struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Region   string `json:"region"`
}

func (c *Client) Me(ctx context.Context, token string) (Profile, error) {
	var profile Profile
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/me", token: token, want: http.StatusOK}, &profile)
	return profile, err
}

type MemoryPage struct {
	Items []json.RawMessage `json:"items"`
}

func (c *Client) MemoriesMonth(ctx context.Context, token, month string) (MemoryPage, error) {
	var page MemoryPage
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/memories?month=" + month, token: token, want: http.StatusOK}, &page)
	return page, err
}
