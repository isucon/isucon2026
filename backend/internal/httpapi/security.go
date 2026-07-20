package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

var alternateCredentialNames = map[string]struct{}{
	"token": {}, "access_token": {}, "session": {}, "session_token": {}, "authorization": {}, "admin_token": {},
}

// SecurityContract enforces the public browser/HTTP boundary without reading
// request bodies, preserving streaming image and sanitizer isolation.
func SecurityContract(publicOrigin string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			request := c.Request()
			path := request.URL.Path
			if strings.HasPrefix(path, "/api/v1/") {
				applyPrivateResponseHeaders(c.Response().Header())
				removeCORSHeaders(c.Response().Header())
				if request.Header.Get(echo.HeaderCookie) != "" || queryHasAlternateCredential(request.URL.Query()) || isURLEncodedFormRequest(request.Header.Get(echo.HeaderContentType)) {
					return BadRequest("credential_source_not_allowed", nil)
				}
				if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
					return Forbidden("cross_origin_request")
				}
				if changesState(request.Method) {
					if origin := request.Header.Get(echo.HeaderOrigin); origin != "" && origin != publicOrigin {
						return Forbidden("origin_not_allowed")
					}
				}
			}
			if err := next(c); err != nil {
				return err
			}
			if strings.HasPrefix(path, "/api/v1/") {
				removeCORSHeaders(c.Response().Header())
			}
			return nil
		}
	}
}

func applyPrivateResponseHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

func removeCORSHeaders(header http.Header) {
	for _, name := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers", "Access-Control-Allow-Methods", "Access-Control-Expose-Headers"} {
		header.Del(name)
	}
}

func changesState(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func queryHasAlternateCredential(values map[string][]string) bool {
	for name := range values {
		if _, forbidden := alternateCredentialNames[strings.ToLower(name)]; forbidden {
			return true
		}
	}
	return false
}

func isURLEncodedFormRequest(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return mediaType == "application/x-www-form-urlencoded"
}

// rejectMultipartCredentialSource runs after the endpoint has parsed its
// multipart body, so legitimate image uploads remain streamable while
// alternate credential fields are still rejected.
func rejectMultipartCredentialSource(request *http.Request) error {
	if request.MultipartForm == nil {
		return nil
	}
	for name := range request.MultipartForm.Value {
		if _, forbidden := alternateCredentialNames[strings.ToLower(name)]; forbidden {
			return BadRequest("credential_source_not_allowed", nil)
		}
	}
	for name := range request.MultipartForm.File {
		if _, forbidden := alternateCredentialNames[strings.ToLower(name)]; forbidden {
			return BadRequest("credential_source_not_allowed", nil)
		}
	}
	return nil
}

const SPAPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; worker-src 'self' blob:; media-src 'self' blob:"
const CameraPermissionsPolicy = "camera=(self)"
