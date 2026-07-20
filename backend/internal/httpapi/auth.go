package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

const principalContextKey = "user_principal"
const administratorContextKey = "administrator_principal"

type AuthHandler struct {
	sessions *service.SessionService
	logger   *slog.Logger
}

func NewAuthHandler(sessions *service.SessionService, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{sessions: sessions, logger: logger}
}

type registerRequest struct{ Username, Password, DisplayName, Region string }
type loginRequest struct{ Username, Password string }

func (h *AuthHandler) Register(c *echo.Context) error {
	var input registerRequest
	if err := DecodeJSON(c, &input); err != nil {
		h.log(c, "register", "input_error", nil)
		return err
	}
	result, err := h.sessions.Register(c.Request().Context(), service.RegistrationInput(input))
	if err != nil {
		h.log(c, "register", authResult(err), nil)
		return authHTTPError(err)
	}
	h.log(c, "register", "success", &result.User)
	return c.JSON(http.StatusCreated, sessionResponse(result))
}

func (h *AuthHandler) Login(c *echo.Context) error {
	var input loginRequest
	if err := DecodeJSON(c, &input); err != nil {
		h.log(c, "login", "input_error", nil)
		return err
	}
	result, err := h.sessions.Login(c.Request().Context(), service.LoginInput(input))
	if err != nil {
		h.log(c, "login", authResult(err), nil)
		return authHTTPError(err)
	}
	h.log(c, "login", "success", &result.User)
	return c.JSON(http.StatusOK, sessionResponse(result))
}

func (h *AuthHandler) Logout(c *echo.Context) error {
	token, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
	if err != nil {
		h.log(c, "logout", "authentication_failed", nil)
		return Unauthorized("invalid_session")
	}
	if err := h.sessions.Logout(c.Request().Context(), token); err != nil {
		h.log(c, "logout", "authentication_failed", nil)
		return Unauthorized("invalid_session")
	}
	h.log(c, "logout", "success", nil)
	return c.NoContent(http.StatusNoContent)
}

type UserAuthenticator interface {
	AuthenticateUser(context.Context, string) (domain.User, error)
}

func UserAuthentication(authenticator UserAuthenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			token, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
			if err != nil {
				return Unauthorized("invalid_session")
			}
			user, err := authenticator.AuthenticateUser(c.Request().Context(), token)
			if err != nil {
				return Unauthorized("invalid_session")
			}
			c.Set(principalContextKey, user)
			return next(c)
		}
	}
}

type AdministratorAuthenticator interface {
	AuthenticateAdministrator(context.Context, string) (domain.Administrator, error)
}

func AdministratorAuthentication(authenticator AdministratorAuthenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			token, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
			if err != nil {
				return Unauthorized("invalid_administrator_session")
			}
			administrator, err := authenticator.AuthenticateAdministrator(c.Request().Context(), token)
			if err != nil {
				return Unauthorized("invalid_administrator_session")
			}
			c.Set(administratorContextKey, administrator)
			return next(c)
		}
	}
}

func AdministratorPrincipal(c *echo.Context) (domain.Administrator, bool) {
	administrator, ok := c.Get(administratorContextKey).(domain.Administrator)
	return administrator, ok
}

func Principal(c *echo.Context) (domain.User, bool) {
	user, ok := c.Get(principalContextKey).(domain.User)
	return user, ok
}

func bearerToken(header string) (string, error) {
	parts := strings.Split(header, " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", service.ErrInvalidSession
	}
	return parts[1], nil
}

func authHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		return Unauthorized("invalid_credentials")
	case errors.Is(err, service.ErrUsernameTaken):
		return Conflict("username_taken")
	case errors.Is(err, service.ErrInactiveAccount):
		return Unauthorized("invalid_credentials")
	default:
		return BadRequest("invalid_registration", err)
	}
}

func authResult(err error) string {
	if errors.Is(err, service.ErrInvalidCredentials) || errors.Is(err, service.ErrInactiveAccount) {
		return "authentication_failed"
	}
	if errors.Is(err, service.ErrUsernameTaken) {
		return "conflict"
	}
	return "input_error"
}

func sessionResponse(result service.SessionResult) map[string]any {
	return map[string]any{"token": result.Token, "user": map[string]any{
		"id": result.User.ID, "username": result.User.Username, "displayName": result.User.DisplayName,
		"bio": result.User.Bio, "region": result.User.Region, "pendingRegion": nil,
		"accountType": result.User.AccountType, "status": result.User.Status,
	}}
}

func (h *AuthHandler) log(c *echo.Context, operation, result string, user *domain.User) {
	attributes := []any{"operation", operation, "result", result, "correlationId", CorrelationIDFrom(c)}
	if user != nil {
		attributes = append(attributes, "userId", user.ID.String())
	}
	h.logger.Info("authentication", attributes...)
}
