package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

type ScopeResolver func(*echo.Context, []byte) (idempotency.Scope, error)

func RequireIdempotency(store idempotency.Store, operation string, resolve ScopeResolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			key, err := uuid.Parse(c.Request().Header.Get("Idempotency-Key"))
			if err != nil {
				return BadRequest("invalid_idempotency_key", errors.New("Idempotency-Key must be a UUID"))
			}
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return BadRequest("invalid_request_body", err)
			}
			c.Request().Body = io.NopCloser(bytes.NewReader(body))
			scope, err := resolve(c, body)
			if err != nil {
				return err
			}
			request := idempotency.Request{Scope: scope, Operation: operation, Key: key, RequestHash: requestFingerprint(c.Request(), body)}
			result, _, err := store.Execute(c.Request().Context(), request, func(ctx context.Context) (idempotency.Result, error) {
				original := c.Response()
				capture := newCaptureWriter()
				c.SetResponse(capture)
				c.SetRequest(c.Request().WithContext(ctx))
				handlerErr := next(c)
				if handlerErr != nil {
					problem := problemFromError(handlerErr, CorrelationIDFrom(c))
					capture = newCaptureWriter()
					capture.Header().Set(echo.HeaderContentType, "application/problem+json")
					capture.WriteHeader(problem.Status)
					_ = json.NewEncoder(capture).Encode(problem)
				}
				c.SetResponse(original)
				return idempotency.Result{Status: capture.status, Header: capture.header, Body: capture.body.Bytes()}, nil
			})
			if errors.Is(err, idempotency.ErrRequestConflict) {
				return Conflict("idempotency_key_reused")
			}
			if errors.Is(err, idempotency.ErrInFlight) {
				return Conflict("idempotency_request_in_flight")
			}
			if err != nil {
				return err
			}
			copyResult(c.Response(), result)
			return nil
		}
	}
}

func UserCallerScope(c *echo.Context, _ []byte) (idempotency.Scope, error) {
	user, ok := Principal(c)
	if !ok {
		return idempotency.Scope{}, Unauthorized("invalid_session")
	}
	return idempotency.Scope{Type: "user", Key: user.ID.String()}, nil
}

func AdministratorCallerScope(c *echo.Context, _ []byte) (idempotency.Scope, error) {
	administrator, ok := AdministratorPrincipal(c)
	if !ok {
		return idempotency.Scope{}, Unauthorized("invalid_administrator_session")
	}
	return idempotency.Scope{Type: "admin", Key: administrator.ID.String()}, nil
}

func UnauthenticatedCallerScope(operation service.AuthOperation) ScopeResolver {
	return func(_ *echo.Context, body []byte) (idempotency.Scope, error) {
		var input struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			return idempotency.Scope{}, BadRequest("invalid_json", err)
		}
		scope, err := service.UnauthenticatedCallerScope(operation, input.Username)
		if err != nil {
			return idempotency.Scope{}, BadRequest("invalid_username", err)
		}
		return idempotency.Scope{Type: scope.Type, Key: scope.Key}, nil
	}
}

func requestFingerprint(request *http.Request, body []byte) string {
	hash := sha256.New()
	hash.Write([]byte(request.Method))
	hash.Write([]byte{0})
	hash.Write([]byte(request.URL.Path))
	hash.Write([]byte{0})
	hash.Write([]byte(canonicalQuery(request.URL.Query())))
	hash.Write([]byte{0})
	hash.Write([]byte(request.Header.Get(echo.HeaderContentType)))
	hash.Write([]byte{0})
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		entries := append([]string(nil), values[key]...)
		sort.Strings(entries)
		for _, value := range entries {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: make(http.Header), status: http.StatusOK}
}
func (w *captureWriter) Header() http.Header            { return w.header }
func (w *captureWriter) WriteHeader(status int)         { w.status = status }
func (w *captureWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

func copyResult(writer http.ResponseWriter, result idempotency.Result) {
	for key := range writer.Header() {
		writer.Header().Del(key)
	}
	for key, values := range result.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}
