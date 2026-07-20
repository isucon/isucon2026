package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

const correlationContextKey = "correlation_id"

type Problem struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Code          string         `json:"code"`
	CorrelationID string         `json:"correlationId"`
	Errors        []ProblemError `json:"errors,omitempty"`
}

type ProblemError struct {
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

type AppError struct {
	Status int
	Code   string
	Title  string
	Cause  error
	Errors []ProblemError
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	return e.Code
}

func (e *AppError) Unwrap() error { return e.Cause }

func BadRequest(code string, cause error) *AppError {
	return &AppError{Status: http.StatusBadRequest, Code: code, Title: "Bad Request", Cause: cause}
}
func Unauthorized(code string) *AppError {
	return &AppError{Status: http.StatusUnauthorized, Code: code, Title: "Unauthorized"}
}
func Forbidden(code string) *AppError {
	return &AppError{Status: http.StatusForbidden, Code: code, Title: "Forbidden"}
}
func NotFound(code string) *AppError {
	return &AppError{Status: http.StatusNotFound, Code: code, Title: "Not Found"}
}
func Conflict(code string) *AppError {
	return &AppError{Status: http.StatusConflict, Code: code, Title: "Conflict"}
}
func PayloadTooLarge() *AppError {
	return &AppError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Title: "Payload Too Large"}
}
func UnsupportedMedia() *AppError {
	return &AppError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Title: "Unsupported Media Type"}
}
func ServiceUnavailable(code string, cause error) *AppError {
	return &AppError{Status: http.StatusServiceUnavailable, Code: code, Title: "Service Unavailable", Cause: cause}
}

func CorrelationID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			id := c.Request().Header.Get("X-Correlation-ID")
			if _, err := uuid.Parse(id); err != nil {
				id = uuid.NewString()
			}
			c.Set(correlationContextKey, id)
			c.Response().Header().Set("X-Correlation-ID", id)
			return next(c)
		}
	}
}

func RequestSizeLimit(maxBytes int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			request := c.Request()
			if request.ContentLength > maxBytes && request.ContentLength >= 0 {
				return PayloadTooLarge()
			}
			request.Body = http.MaxBytesReader(c.Response(), request.Body, maxBytes)
			return next(c)
		}
	}
}

func Recover(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("panic recovered", "operation", c.Path(), "result", "unexpected_error", "correlationId", CorrelationIDFrom(c), "panicType", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
					err = &AppError{Status: http.StatusInternalServerError, Code: "internal_error", Title: "Internal Server Error"}
				}
			}()
			return next(c)
		}
	}
}

func CorrelationIDFrom(c *echo.Context) string {
	if id, ok := c.Get(correlationContextKey).(string); ok {
		return id
	}
	return uuid.Nil.String()
}

// DecodeJSON accepts exactly one JSON object and rejects unknown fields,
// trailing values, empty bodies, and non-JSON media types.
func DecodeJSON(c *echo.Context, destination any) error {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || mediaType != echo.MIMEApplicationJSON {
		return UnsupportedMedia()
	}
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return PayloadTooLarge()
		}
		return BadRequest("invalid_json", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("invalid_json", errors.New("request body must contain exactly one JSON value"))
	}
	return nil
}

func ErrorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		problem := problemFromError(err, CorrelationIDFrom(c))
		if problem.Status >= 500 {
			logger.Error("request failed", "operation", c.Path(), "result", "unexpected_error", "correlationId", problem.CorrelationID, "errorType", fmt.Sprintf("%T", err))
		}
		c.Response().Header().Set(echo.HeaderContentType, "application/problem+json")
		if writeErr := c.JSON(problem.Status, problem); writeErr != nil {
			logger.Error("problem response failed", "operation", c.Path(), "result", "write_error", "correlationId", problem.CorrelationID)
		}
	}
}

func problemFromError(err error, correlationID string) Problem {
	var appError *AppError
	if errors.As(err, &appError) {
		return Problem{Type: "https://isureal.invalid/problems/" + appError.Code, Title: appError.Title, Status: appError.Status, Code: appError.Code, CorrelationID: correlationID, Errors: appError.Errors}
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return Problem{Type: "https://isureal.invalid/problems/payload_too_large", Title: "Payload Too Large", Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", CorrelationID: correlationID}
	}
	var echoError *echo.HTTPError
	if errors.As(err, &echoError) {
		status := echoError.Code
		code := strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
		if code == "" {
			status, code = http.StatusInternalServerError, "internal_error"
		}
		return Problem{Type: "https://isureal.invalid/problems/" + code, Title: http.StatusText(status), Status: status, Code: code, CorrelationID: correlationID}
	}
	return Problem{Type: "https://isureal.invalid/problems/internal_error", Title: "Internal Server Error", Status: http.StatusInternalServerError, Code: "internal_error", CorrelationID: correlationID}
}
