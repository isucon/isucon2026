package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
	"github.com/isucon/isucon2026/backend/internal/observability"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

func TestStructuredLogAuditExercisesOutcomesAndRejectsSensitiveData(t *testing.T) {
	ctx := context.Background()
	pool, _ := us1Database(t)
	db := &postgres.DB{Pool: pool}
	fake := clock.NewFake(us1Now)
	var output bytes.Buffer
	logger := observability.New(&output, slog.LevelDebug)
	ids := domain.RandomIDGenerator{}
	sessions := postgres.NewSessionRepository(db, ids, fake)
	idempotency := postgres.NewIdempotencyRepository(db, ids, fake)
	relationships := postgres.NewRelationshipRepository(db, ids, fake)
	notifications := postgres.NewNotificationRepository(db, ids, fake)
	e := httpapi.NewRouter(httpapi.RouterDependencies{
		DB: db, Logger: logger, Sessions: service.NewSessionService(sessions), SessionStore: sessions,
		Administrators:    service.NewAdminService(sessions, relationships),
		Schedules:         service.NewScheduleService(notifications, fake),
		PushSubscriptions: service.NewPushSubscriptionService(notifications),
		Idempotency:       idempotency,
	})

	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		if token != "" {
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, req)
		return recorder
	}

	const content = "LOG_CONTENT_SECRET"
	registered := request(http.MethodPost, "/api/v1/auth/register", fmt.Sprintf(`{"username":"audit-user","displayName":%q,"region":"asia"}`, content), "")
	if registered.Code != http.StatusCreated {
		t.Fatalf("register=%d %s", registered.Code, registered.Body.String())
	}
	var session struct {
		Token     string `json:"token"`
		SecretKey string `json:"secretKey"`
	}
	if err := json.Unmarshal(registered.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.SecretKey == "" {
		t.Fatal("register response omitted secretKey")
	}
	conflict := request(http.MethodPost, "/api/v1/auth/register", `{"username":"AUDIT-USER","displayName":"other","region":"asia"}`, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	input := request(http.MethodPost, "/api/v1/auth/login", `{"username":"audit-user","secretKey":"bad","unexpected":true}`, "")
	if input.Code != http.StatusBadRequest {
		t.Fatalf("input=%d", input.Code)
	}
	authFailure := request(http.MethodPost, "/api/v1/auth/login", `{"username":"audit-user","secretKey":"wrong-secret"}`, "")
	if authFailure.Code != http.StatusUnauthorized {
		t.Fatalf("auth failure=%d", authFailure.Code)
	}

	const pushEndpoint = "https://push.invalid/LOG_ENDPOINT_SECRET"
	const p256dh = "LOG_P256DH_SECRET"
	const pushAuth = "LOG_PUSH_AUTH_SECRET"
	push := request(http.MethodPut, "/api/v1/me/push-subscription", fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`, pushEndpoint, p256dh, pushAuth), session.Token)
	if push.Code != http.StatusNoContent {
		t.Fatalf("push=%d %s", push.Code, push.Body.String())
	}

	const adminPassword = "LOG_ADMIN_PASSWORD_SECRET"
	adminHash := service.HashCredential(adminPassword)
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO administrators(id,login_name_normalized,password_hash,enabled,created_at)VALUES($1,'audit-admin',$2,true,$3)`, adminID, adminHash, us1Now); err != nil {
		t.Fatal(err)
	}
	adminLogin := request(http.MethodPost, "/api/v1/admin/auth/login", fmt.Sprintf(`{"username":"audit-admin","password":%q}`, adminPassword), "")
	if adminLogin.Code != http.StatusOK {
		t.Fatalf("admin login=%d %s", adminLogin.Code, adminLogin.Body.String())
	}
	var adminSession struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(adminLogin.Body.Bytes(), &adminSession); err != nil {
		t.Fatal(err)
	}
	scheduleAt := us1Now.Add(time.Hour).Format(time.RFC3339)
	schedule := request(http.MethodPut, "/api/v1/admin/regions/asia/schedule", fmt.Sprintf(`{"scheduledAt":%q}`, scheduleAt), adminSession.Token)
	if schedule.Code != http.StatusOK {
		t.Fatalf("schedule=%d %s", schedule.Code, schedule.Body.String())
	}
	scheduleConflict := request(http.MethodPut, "/api/v1/admin/regions/asia/schedule", fmt.Sprintf(`{"scheduledAt":%q}`, us1Now.Add(-time.Hour).Format(time.RFC3339)), adminSession.Token)
	if scheduleConflict.Code != http.StatusConflict {
		t.Fatalf("schedule conflict=%d", scheduleConflict.Code)
	}
	certify := request(http.MethodPut, "/api/v1/admin/users/"+uuid.NewString()+"/certification", "", adminSession.Token)
	if certify.Code != http.StatusConflict {
		t.Fatalf("certify missing=%d", certify.Code)
	}

	errorRequest := httptest.NewRequest(http.MethodGet, "/unexpected", nil)
	errorRecorder := httptest.NewRecorder()
	errorContext := e.NewContext(errorRequest, errorRecorder)
	errorContext.SetPath("/unexpected")
	e.HTTPErrorHandler(errorContext, errors.New("LOG_UNEXPECTED_SECRET"))

	logs := output.String()
	for _, secret := range []string{session.SecretKey, content, session.Token, adminPassword, adminSession.Token, pushEndpoint, p256dh, pushAuth, "LOG_UNEXPECTED_SECRET", "caption", "biography", "35.681236", "139.767125"} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("secret leaked in logs: %q", secret)
		}
	}
	for _, operation := range []string{"register", "login", "admin_login", "set_schedule", "certify_user", "/unexpected"} {
		if !strings.Contains(logs, `"operation":"`+operation+`"`) {
			t.Errorf("operation not logged: %s", operation)
		}
	}
	for _, result := range []string{"success", "conflict", "input_error", "authentication_failed", "unexpected_error"} {
		if !strings.Contains(logs, `"result":"`+result+`"`) {
			t.Errorf("result not logged: %s", result)
		}
	}
}
