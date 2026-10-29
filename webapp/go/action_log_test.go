package isureal

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestActionLogMiddleware(t *testing.T) {
	newServer := func(minLevel string) (*echo.Echo, *bytes.Buffer) {
		var output bytes.Buffer
		e := echo.New()
		e.IPExtractor = extractClientIP
		e.HTTPErrorHandler = APIErrorHandler
		e.Use(ActionLogMiddleware(minLevel, &output))
		e.POST("/api/auth/login", func(c *echo.Context) error {
			var input loginRequest
			if err := c.Bind(&input); err != nil {
				return err
			}
			c.Set("username", input.Username)
			return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
		})
		e.POST("/api/posts", func(c *echo.Context) error {
			c.Set("session", &Session{UserID: "user-1"})
			return c.NoContent(http.StatusCreated)
		})
		e.GET("/api/feeds/:feed", func(c *echo.Context) error {
			c.Set("session", &Session{UserID: "user-1"})
			return c.NoContent(http.StatusOK)
		})
		e.GET("/api/users/:userId", func(c *echo.Context) error {
			return errors.New("boom")
		})
		e.PUT("/api/me/profile-image", func(c *echo.Context) error {
			if c.Request().Header.Get("X-Early") == "" {
				c.Set("abuse", "invalid_image")
			}
			return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
		})
		e.GET("/api/images/:imageId", func(c *echo.Context) error {
			c.Set("abuse", "invalid_signature")
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		})
		return e, &output
	}
	send := func(e *echo.Echo, method, path, body string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Client-IP", "203.0.113.7")
		e.ServeHTTP(httptest.NewRecorder(), req)
	}
	lines := func(output *bytes.Buffer) []map[string]any {
		var entries []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("not JSON: %q", line)
			}
			entries = append(entries, entry)
		}
		return entries
	}

	e, output := newServer("debug")
	send(e, http.MethodPost, "/api/auth/login", `{"username":"alice","secretKey":"wrong"}`)
	send(e, http.MethodPost, "/api/auth/login", `{"username":"Not Valid","secretKey":"wrong"}`)
	send(e, http.MethodPost, "/api/posts", `{}`)
	send(e, http.MethodGet, "/api/feeds/friends", "")
	send(e, http.MethodGet, "/api/users/someone", "")
	send(e, http.MethodGet, "/api/unknown", "")
	want := []map[string]any{
		{"level": "info", "action": "login", "status": 401.0, "ip": "203.0.113.7", "user_id": nil, "username": "alice"},
		{"level": "info", "action": "login", "status": 401.0, "ip": "203.0.113.7", "user_id": nil, "username": nil},
		{"level": "info", "action": "create_post", "status": 201.0, "ip": "203.0.113.7", "user_id": "user-1"},
		{"level": "debug", "action": "get_feed", "status": 200.0, "ip": "203.0.113.7", "user_id": "user-1"},
		{"level": "error", "action": "get_user", "status": 500.0, "ip": "203.0.113.7", "user_id": nil},
		{"level": "debug", "action": nil, "status": 404.0, "ip": "203.0.113.7", "user_id": nil},
	}
	if got := lines(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("logs:\n got=%v\nwant=%v", got, want)
	}

	// info では読み取り（GET）の行を出さない。
	e, output = newServer("info")
	send(e, http.MethodGet, "/api/feeds/friends", "")
	send(e, http.MethodPost, "/api/posts", `{}`)
	send(e, http.MethodPut, "/api/me/profile-image", "")
	send(e, http.MethodGet, "/api/images/image-id", "")
	large := httptest.NewRequest(http.MethodPut, "/api/me/profile-image", strings.NewReader(""))
	large.Header.Set("X-Early", "1")
	large.Header.Set("X-Client-IP", "203.0.113.7")
	large.ContentLength = 31 << 20
	e.ServeHTTP(httptest.NewRecorder(), large)
	if got := lines(output); len(got) != 4 || got[0]["action"] != "create_post" || got[1]["abuse"] != "invalid_image" || got[2]["abuse"] != "invalid_signature" || got[2]["level"] != "info" || got[3]["abuse"] != "invalid_image" {
		t.Fatalf("info level logs = %v", got)
	}
}
