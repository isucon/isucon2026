package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/isucon/isucon2026/backend/internal/idempotency"
	"github.com/labstack/echo/v5"
)

type OpenAPIValidator struct {
	document *openapi3.T
	router   routers.Router
}

func LoadOpenAPI(ctx context.Context, filename string) (*OpenAPIValidator, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filename)
	if err != nil {
		return nil, fmt.Errorf("load OpenAPI document: %w", err)
	}
	if err := document.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate OpenAPI document: %w", err)
	}
	router, err := legacy.NewRouter(document)
	if err != nil {
		return nil, fmt.Errorf("build OpenAPI router: %w", err)
	}
	return &OpenAPIValidator{document: document, router: router}, nil
}

func (v *OpenAPIValidator) Document() *openapi3.T { return v.document }

func (v *OpenAPIValidator) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			request := c.Request()
			route, parameters, err := v.router.FindRoute(request)
			if err != nil {
				if request.URL.Path == "/health/live" || request.URL.Path == "/health/ready" {
					return next(c)
				}
				return NotFound("route_not_found")
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return BadRequest("contract_request_invalid", err)
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			validation := &openapi3filter.RequestValidationInput{
				Request: request, PathParams: parameters, Route: route,
				Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
			}
			if err := openapi3filter.ValidateRequest(request.Context(), validation); err != nil {
				return BadRequest("contract_request_invalid", err)
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			original := c.Response()
			capture := newCaptureWriter()
			c.SetResponse(capture)
			handlerErr := next(c)
			if handlerErr != nil {
				problem := problemFromError(handlerErr, CorrelationIDFrom(c))
				capture = newCaptureWriter()
				capture.Header().Set(echo.HeaderContentType, "application/problem+json")
				capture.WriteHeader(problem.Status)
				if err := json.NewEncoder(capture).Encode(problem); err != nil {
					c.SetResponse(original)
					return err
				}
			}
			responseValidation := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: validation,
				Status:                 capture.status,
				Header:                 capture.header,
				Options:                &openapi3filter.Options{IncludeResponseStatus: true},
			}
			responseValidation.SetBodyBytes(capture.body.Bytes())
			if err := openapi3filter.ValidateResponse(request.Context(), responseValidation); err != nil {
				c.SetResponse(original)
				return &AppError{Status: http.StatusInternalServerError, Code: "contract_response_invalid", Title: "Internal Server Error", Cause: err}
			}
			c.SetResponse(original)
			copyResult(original, idempotency.Result{Status: capture.status, Header: capture.header, Body: capture.body.Bytes()})
			return nil
		}
	}
}
