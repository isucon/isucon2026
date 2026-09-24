package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const (
	apiPrefix          = "/api"
	requestTimeout     = 15 * time.Second
	maxErrorBodyBytes  = 1 << 20
	multipartImageName = "photo.jpg"
)

type Client struct {
	base    string
	host    string
	country string
	http    *http.Client
}

func (c *Client) SetCountry(country string) { c.country = country }

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
		// Sessions use bearer tokens.
		http: &http.Client{Timeout: requestTimeout},
	}
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
	contentType string
	body        []byte
	want        int
	timeout     time.Duration
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	httpRequest, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, bytes.NewReader(r.body))
	if err != nil {
		return err
	}
	if c.host != "" {
		httpRequest.Host = c.host
	}
	httpRequest.Header.Set("X-Country-Code", c.country)
	if r.contentType != "" {
		httpRequest.Header.Set("Content-Type", r.contentType)
	}
	if r.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.token)
	}
	httpClient := *c.http
	if r.timeout > 0 {
		httpClient.Timeout = r.timeout
	}
	response, err := httpClient.Do(httpRequest)
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
		Code string `json:"error"`
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
	Language string `json:"language"`
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	var result InitializeResult
	err := c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/initialize", want: http.StatusOK, timeout: 30 * time.Second}, &result)
	return result, err
}

// --- auth ---

type User struct {
	ID        string
	Username  string
	Token     string
	SecretKey string
}

type sessionResponse struct {
	SecretKey string `json:"secretKey"`
	Token     string `json:"token"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

func (s sessionResponse) user() User {
	return User{ID: s.User.ID, Username: s.User.Username, Token: s.Token, SecretKey: s.SecretKey}
}

func (c *Client) Register(ctx context.Context, username, displayName, region string) (User, error) {
	body := marshalJSON(map[string]string{
		"username": username, "displayName": displayName, "region": region,
	})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/register",
		contentType: "application/json", body: body, want: http.StatusCreated,
	}, &session)
	return session.user(), err
}

func (c *Client) Login(ctx context.Context, username, secretKey string) (User, error) {
	body := marshalJSON(map[string]string{"username": username, "secretKey": secretKey})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/login",
		contentType: "application/json", body: body, want: http.StatusOK,
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
		contentType: "application/json", body: body, want: http.StatusOK,
	}, &result)
	return result.Token, err
}

func (c *Client) OpenWindow(ctx context.Context, token, region, date string) (Window, error) {
	var window Window
	err := c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/admin/posting-window", token: token, contentType: "application/json", body: marshalJSON(map[string]string{"region": region, "localDate": date}), want: http.StatusCreated}, &window)
	return window, err
}

func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/auth/logout", token: token, want: http.StatusNoContent}, nil)
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
		map[string]string{"caption": caption, "visibility": visibility, "location_kind": "none"},
		[]filePart{
			{field: "image", filename: multipartImageName, contentType: "image/jpeg", data: image},
		},
	)
	if err != nil {
		return Post{}, err
	}
	var post Post
	err = c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts",
		token: token, contentType: contentType, body: body, want: http.StatusCreated,
	}, &post)
	return post, err
}

type FeedPage struct {
	Items   []Post `json:"items"`
	HasMore bool   `json:"hasMore"`
}

func (c *Client) Feed(ctx context.Context, token, feed string, before *Post) (FeedPage, error) {
	path := apiPrefix + "/feeds/" + feed
	if before != nil {
		path += "?" + url.Values{"beforePostedAt": {before.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page FeedPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}

// --- interactions ---

func (c *Client) CreateComment(ctx context.Context, token, postID, body string) error {
	return c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts/" + postID + "/comments",
		token: token, contentType: "application/json",
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
		token: token, contentType: contentType, body: body, want: http.StatusCreated,
	}, nil)
}

// --- reads ---

type Notification struct {
	ID              string `json:"id"`
	PostingWindowID string `json:"postingWindowId"`
	Region          string `json:"region"`
}

type NotificationStream struct{ body io.ReadCloser }

// OpenNotifications returns after the server has established the subscription.
func (c *Client) OpenNotifications(ctx context.Context, token string) (*NotificationStream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+apiPrefix+"/notifications", nil)
	if err != nil {
		return nil, err
	}
	if c.host != "" {
		req.Host = c.host
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Country-Code", c.country)
	streaming := *c.http
	streaming.Timeout = 0
	res, err := streaming.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBodyBytes))
		return nil, &APIError{Method: http.MethodGet, Path: apiPrefix + "/notifications", Status: res.StatusCode, Want: http.StatusOK, Code: problemCode(data)}
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		res.Body.Close()
		return nil, fmt.Errorf("notifications: expected text/event-stream, got %q", res.Header.Get("Content-Type"))
	}
	return &NotificationStream{body: res.Body}, nil
}

func (s *NotificationStream) Close() error { return s.body.Close() }
func (s *NotificationStream) Receive() (Notification, error) {
	scanner := bufio.NewScanner(s.body)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && len(data) > 0 {
			var notification Notification
			err := json.Unmarshal([]byte(strings.Join(data, "\n")), &notification)
			return notification, err
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return Notification{}, err
	}
	return Notification{}, io.EOF
}

func (c *Client) Image(ctx context.Context, token, path string) ([]byte, error) {
	if !strings.HasPrefix(path, apiPrefix+"/images/") {
		return nil, fmt.Errorf("invalid image URL %q", path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.host != "" {
		req.Host = c.host
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Country-Code", c.country)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Method: http.MethodGet, Path: path, Status: res.StatusCode, Want: http.StatusOK}
	}
	return io.ReadAll(io.LimitReader(res.Body, 16<<20))
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
	Items   []Post `json:"items"`
	HasMore bool   `json:"hasMore"`
}

func (c *Client) MemoriesMonth(ctx context.Context, token, month string, before *Post) (MemoryPage, error) {
	path := apiPrefix + "/memories?month=" + url.QueryEscape(month)
	if before != nil {
		path += "&" + url.Values{"beforePostedAt": {before.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page MemoryPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}
