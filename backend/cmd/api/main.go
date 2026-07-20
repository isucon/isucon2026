package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/config"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/observability"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

func main() {
	logger := observability.New(os.Stdout, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "operation", "startup", "result", "configuration_error", "correlationId", "00000000-0000-0000-0000-000000000000", "errorType", "configuration")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database startup failed", "operation", "startup", "result", "database_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
	defer db.Close()
	source := clock.System{}
	ids := domain.RandomIDGenerator{}
	sessionRepository := postgres.NewSessionRepository(db, ids, source)
	idempotencyRepository := postgres.NewIdempotencyRepository(db, ids, source)
	postRepository := postgres.NewPostRepository(db)
	interactionRepository := postgres.NewInteractionRepository(db)
	profileRepository := postgres.NewProfileRepository(db, source)
	accountRepository := postgres.NewAccountRepository(db, source)
	memoryRepository := postgres.NewMemoryRepository(db)
	notificationRepository := postgres.NewNotificationRepository(db, ids, source)
	relationshipRepository := postgres.NewRelationshipRepository(db, ids, source)
	userRepository := postgres.NewUserRepository(db)
	visibility := service.NewVisibilityPolicy(postRepository)
	sanitizer := isurealimage.NewIsolatedSanitizer(
		isurealimage.NewSubprocessProcess(cfg.Sanitizer.Path), cfg.Sanitizer.Concurrency,
		isurealimage.ResourceBudget{MemoryBytes: cfg.Sanitizer.MemoryBytes, CPUSeconds: cfg.Sanitizer.CPUSeconds, Timeout: cfg.Sanitizer.Timeout},
	)
	imageStore := isurealimage.NewStore(cfg.ImageRoot, sanitizer, ids)
	postService := service.NewPostService(postRepository, visibility, imageStore, source, ids)
	interactionService := service.NewInteractionService(interactionRepository, visibility, imageStore, source, ids)
	accountService := service.NewAccountService(profileRepository, accountRepository, imageStore)
	cursorCodec, err := httpapi.NewCursorCodec([]byte(cfg.CursorHMACKey))
	if err != nil {
		logger.Error("cursor startup failed", "operation", "startup", "result", "configuration_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
	contractPath := os.Getenv("ISUREAL_OPENAPI_PATH")
	if contractPath == "" {
		contractPath = filepath.Join("..", "specs", "001-isureal-social-app", "contracts", "openapi.yaml")
	}
	contract, err := httpapi.LoadOpenAPI(ctx, contractPath)
	if err != nil {
		logger.Error("contract startup failed", "operation", "startup", "result", "contract_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
	e := httpapi.NewRouter(httpapi.RouterDependencies{
		DB: db, Logger: logger, Sessions: service.NewSessionService(sessionRepository), SessionStore: sessionRepository, PublicOrigin: cfg.PublicOrigin,
		Posts: postService, Interactions: interactionService, Accounts: accountService, Memories: service.NewMemoryService(memoryRepository), Notifications: service.NewNotificationService(notificationRepository), PushSubscriptions: service.NewPushSubscriptionService(notificationRepository), Schedules: service.NewScheduleService(notificationRepository, source), Relationships: service.NewRelationshipService(relationshipRepository, userRepository), Administrators: service.NewAdminService(sessionRepository, relationshipRepository), Images: imageStore, Cursor: cursorCodec, Idempotency: idempotencyRepository, OpenAPI: contract,
	})
	start := echo.StartConfig{Address: cfg.ListenAddress}
	if err := start.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("API stopped unexpectedly", "operation", "shutdown", "result", "unexpected_error", "correlationId", "00000000-0000-0000-0000-000000000000")
		os.Exit(1)
	}
}
