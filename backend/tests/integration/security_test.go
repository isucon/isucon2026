package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/httpapi"
	"github.com/labstack/echo/v5"
)

func securityHarness() *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = httpapi.ErrorHandler(e.Logger)
	e.Use(httpapi.CorrelationID())
	e.Use(httpapi.SecurityContract("https://app.example.test"))
	e.GET("/api/v1/private", func(c *echo.Context) error { return c.JSON(http.StatusOK, map[string]bool{"ok": true}) })
	e.POST("/api/v1/private", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	e.GET("/api/v1/images/:id", func(c *echo.Context) error {
		return c.Blob(http.StatusOK, "image/jpeg", []byte{0xff, 0xd8, 0xff, 0xd9})
	})
	e.GET("/api/v1/error", func(*echo.Context) error { return httpapi.Unauthorized("invalid_session") })
	return e
}

func securityRequest(e *echo.Echo, method, target, contentType string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader("token=form-secret"))
	if contentType != "" {
		request.Header.Set(echo.HeaderContentType, contentType)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	return recorder
}

func assertPrivateHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for name, value := range map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff"} {
		if response.Header().Get(name) != value {
			t.Errorf("%s=%q", name, response.Header().Get(name))
		}
	}
	for name := range response.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("CORS permission returned: %s", name)
		}
	}
}

func TestSecurityContractBearerSourceBrowserOriginAndHeaders(t *testing.T) {
	e := securityHarness()
	accepted := securityRequest(e, http.MethodPost, "/api/v1/private", "application/json", map[string]string{"Authorization": "Bearer header-token", "Origin": "https://app.example.test", "Sec-Fetch-Site": "same-origin"})
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("same origin=%d", accepted.Code)
	}
	assertPrivateHeaders(t, accepted)
	headerless := securityRequest(e, http.MethodPost, "/api/v1/private", "application/json", map[string]string{"Authorization": "Bearer cli-token"})
	if headerless.Code != http.StatusNoContent {
		t.Fatalf("headerless client=%d", headerless.Code)
	}
	multipart := securityRequest(e, http.MethodPost, "/api/v1/private", "multipart/form-data; boundary=upload", map[string]string{"Authorization": "Bearer header-token", "Origin": "https://app.example.test", "Sec-Fetch-Site": "same-origin"})
	if multipart.Code != http.StatusNoContent {
		t.Fatalf("multipart image upload=%d", multipart.Code)
	}

	cases := []struct {
		name, target, contentType string
		headers                   map[string]string
	}{
		{"cookie", "/api/v1/private", "application/json", map[string]string{"Cookie": "session=cookie-secret"}},
		{"query", "/api/v1/private?access_token=query-secret", "application/json", nil},
		{"form", "/api/v1/private", "application/x-www-form-urlencoded", nil},
		{"origin", "/api/v1/private", "application/json", map[string]string{"Origin": "https://evil.example", "Authorization": "Bearer valid"}},
		{"fetch metadata", "/api/v1/private", "application/json", map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": "Bearer valid"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := securityRequest(e, http.MethodPost, test.target, test.contentType, test.headers)
			if response.Code < 400 || response.Code == http.StatusTooManyRequests {
				t.Fatalf("status=%d", response.Code)
			}
			assertPrivateHeaders(t, response)
		})
	}
	for _, target := range []string{"/api/v1/private", "/api/v1/images/one", "/api/v1/error"} {
		response := securityRequest(e, http.MethodGet, target, "", map[string]string{"Authorization": "Bearer valid"})
		assertPrivateHeaders(t, response)
	}
}

func TestSecurityContractHasNoRateLimitBehavior(t *testing.T) {
	e := securityHarness()
	for index := 0; index < 200; index++ {
		response := securityRequest(e, http.MethodGet, "/api/v1/private", "", map[string]string{"Authorization": "Bearer valid"})
		if response.Code != http.StatusOK {
			t.Fatalf("request %d status=%d", index, response.Code)
		}
		for name := range response.Header() {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "rate") || lower == "retry-after" {
				t.Fatalf("rate-limit header %s", name)
			}
		}
	}
}

func TestSPASecurityPolicyRestrictsBaseObjectFramesAndCamera(t *testing.T) {
	for _, directive := range []string{"default-src 'self'", "base-uri 'none'", "object-src 'none'", "frame-ancestors 'none'", "connect-src 'self'", "worker-src 'self'"} {
		if !strings.Contains(httpapi.SPAPolicy, directive) {
			t.Errorf("missing CSP directive %q", directive)
		}
	}
	if httpapi.CameraPermissionsPolicy != "camera=(self)" {
		t.Fatalf("permissions policy=%q", httpapi.CameraPermissionsPolicy)
	}
}
