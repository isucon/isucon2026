package isureal

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestProfileImageAbuseRequiresInvalidJPEG(t *testing.T) {
	var output bytes.Buffer
	e := echo.New()
	e.IPExtractor = extractClientIP
	e.HTTPErrorHandler = APIErrorHandler
	e.Use(ActionLogMiddleware("info", &output))
	e.PUT("/api/me/profile-image", putProfileImageHandler(&HandlerContext{}))

	var valid bytes.Buffer
	if err := jpeg.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 513, 513)), nil); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{valid.Bytes(), []byte("not a JPEG")} {
		req := httptest.NewRequest(http.MethodPut, "/api/me/profile-image", bytes.NewReader(body))
		req.Header.Set("Content-Type", "image/jpeg")
		req.Header.Set("X-Client-IP", "203.0.113.7")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatalf("expected one abuse log, got %q: %v", output.String(), err)
	}
	if entry["action"] != "put_profile_image" || entry["abuse"] != "invalid_image" || entry["ip"] != "203.0.113.7" || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("unexpected action log: %q", output.String())
	}
}
