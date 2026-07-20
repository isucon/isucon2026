package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/isucon/isucon2026/backend/internal/idempotency"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/labstack/echo/v5"
)

type RouterDependencies struct {
	DB                *postgres.DB
	Logger            *slog.Logger
	Sessions          *service.SessionService
	SessionStore      *postgres.SessionRepository
	Posts             *service.PostService
	Interactions      *service.InteractionService
	Accounts          *service.AccountService
	Memories          *service.MemoryService
	Notifications     *service.NotificationService
	PushSubscriptions *service.PushSubscriptionService
	Schedules         *service.ScheduleService
	Relationships     *service.RelationshipService
	Administrators    *service.AdminService
	Images            *isurealimage.Store
	Cursor            *CursorCodec
	Idempotency       idempotency.Store
	OpenAPI           *OpenAPIValidator
	PublicOrigin      string
}

func NewRouter(dependencies RouterDependencies) *echo.Echo {
	e := echo.New()
	e.Logger = dependencies.Logger
	e.HTTPErrorHandler = ErrorHandler(dependencies.Logger)
	e.Use(CorrelationID())
	e.Use(SecurityContract(dependencies.PublicOrigin))
	e.Use(Recover(dependencies.Logger))
	e.Use(RequestSizeLimit(12 << 20))
	if dependencies.OpenAPI != nil {
		e.Use(dependencies.OpenAPI.Middleware())
	}

	e.GET("/health/live", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"status": "ok"})
	})
	e.GET("/health/ready", func(c *echo.Context) error {
		var version int64
		if err := dependencies.DB.Pool.QueryRow(c.Request().Context(), `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&version); err != nil {
			return ServiceUnavailable("database_unavailable", err)
		}
		return c.JSON(http.StatusOK, map[string]any{"status": "ready", "migrationVersion": version})
	})

	auth := NewAuthHandler(dependencies.Sessions, dependencies.Logger)
	e.POST("/api/v1/auth/register", auth.Register,
		RequireIdempotency(dependencies.Idempotency, "register", UnauthenticatedCallerScope(service.AuthRegistration)))
	e.POST("/api/v1/auth/login", auth.Login,
		RequireIdempotency(dependencies.Idempotency, "login", UnauthenticatedCallerScope(service.AuthUserLogin)))
	e.POST("/api/v1/auth/logout", auth.Logout,
		UserAuthentication(dependencies.SessionStore),
		RequireIdempotency(dependencies.Idempotency, "logout", UserCallerScope))

	if dependencies.Posts != nil && dependencies.Cursor != nil {
		posts := NewPostHandler(dependencies.Posts, dependencies.Cursor, dependencies.Logger)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/posting-window/current", posts.CurrentWindow, authenticate)
		e.POST("/api/v1/posts", posts.Create, authenticate,
			RequireIdempotency(dependencies.Idempotency, "create_post", UserCallerScope))
		e.GET("/api/v1/posts/me/current", posts.CurrentSelf, authenticate)
		e.GET("/api/v1/posts/:postId", posts.Get, authenticate)
		e.PATCH("/api/v1/posts/:postId", posts.Update, authenticate,
			RequireIdempotency(dependencies.Idempotency, "update_post", UserCallerScope))
		e.DELETE("/api/v1/posts/:postId", posts.Delete, authenticate,
			RequireIdempotency(dependencies.Idempotency, "delete_post", UserCallerScope))
		e.GET("/api/v1/feeds/:feed", posts.Feed, authenticate)
		if dependencies.Images != nil && dependencies.Interactions == nil {
			images := NewImageHandler(dependencies.Images, dependencies.Posts)
			e.GET("/api/v1/images/:imageId", images.Get, authenticate)
		}
	}
	if dependencies.Interactions != nil && dependencies.Cursor != nil {
		interactions := NewInteractionHandler(dependencies.Interactions, dependencies.Cursor, dependencies.Logger)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/posts/:postId/realmojis", interactions.ListRealMojis, authenticate)
		e.POST("/api/v1/posts/:postId/realmojis", interactions.ReplaceRealMoji, authenticate, RequireIdempotency(dependencies.Idempotency, "replace_realmoji", UserCallerScope))
		e.DELETE("/api/v1/realmojis/:realMojiId", interactions.DeleteRealMoji, authenticate, RequireIdempotency(dependencies.Idempotency, "delete_realmoji", UserCallerScope))
		e.GET("/api/v1/posts/:postId/comments", interactions.ListComments, authenticate)
		e.POST("/api/v1/posts/:postId/comments", interactions.CreateComment, authenticate, RequireIdempotency(dependencies.Idempotency, "create_comment", UserCallerScope))
		e.DELETE("/api/v1/comments/:commentId", interactions.DeleteComment, authenticate, RequireIdempotency(dependencies.Idempotency, "delete_comment", UserCallerScope))
		if dependencies.Images != nil && dependencies.Posts != nil {
			authorizers := MultiImageAuthorizer{dependencies.Posts, dependencies.Interactions}
			if dependencies.Accounts != nil {
				authorizers = append(authorizers, dependencies.Accounts)
			}
			images := NewImageHandler(dependencies.Images, authorizers)
			e.GET("/api/v1/images/:imageId", images.Get, authenticate)
		}
	}
	if dependencies.Accounts != nil {
		accounts := NewAccountHandler(dependencies.Accounts, dependencies.Logger)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/me", accounts.Get, authenticate)
		e.PATCH("/api/v1/me", accounts.Update, authenticate, RequireIdempotency(dependencies.Idempotency, "update_me", UserCallerScope))
		e.PUT("/api/v1/me/profile-image", accounts.PutImage, authenticate, RequireIdempotency(dependencies.Idempotency, "put_profile_image", UserCallerScope))
		e.DELETE("/api/v1/me/profile-image", accounts.DeleteImage, authenticate, RequireIdempotency(dependencies.Idempotency, "delete_profile_image", UserCallerScope))
		e.POST("/api/v1/me/account-deletion", accounts.DeleteAccount, authenticate, RequireIdempotency(dependencies.Idempotency, "request_account_deletion", UserCallerScope))
	}
	if dependencies.Memories != nil && dependencies.Cursor != nil {
		memories := NewMemoryHandler(dependencies.Memories, dependencies.Cursor)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/memories", memories.Month, authenticate)
		e.GET("/api/v1/memories/:date", memories.Date, authenticate)
	}
	if dependencies.Relationships != nil && dependencies.Cursor != nil {
		relationships := NewRelationshipHandler(dependencies.Relationships, dependencies.Cursor, dependencies.Logger)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/users", relationships.Search, authenticate)
		e.GET("/api/v1/users/:userId", relationships.GetUser, authenticate)
		e.POST("/api/v1/friend-requests", relationships.CreateRequest, authenticate, RequireIdempotency(dependencies.Idempotency, "create_friend_request", UserCallerScope))
		e.POST("/api/v1/friend-requests/:requestId/:action", relationships.Transition, authenticate, RequireIdempotency(dependencies.Idempotency, "transition_friend_request", UserCallerScope))
		e.GET("/api/v1/relationships/:kind", relationships.List, authenticate)
		e.DELETE("/api/v1/friendships/:userId", relationships.DeleteFriendship, authenticate, RequireIdempotency(dependencies.Idempotency, "delete_friendship", UserCallerScope))
		e.PUT("/api/v1/follows/:userId", relationships.Follow, authenticate, RequireIdempotency(dependencies.Idempotency, "follow_user", UserCallerScope))
		e.DELETE("/api/v1/follows/:userId", relationships.Unfollow, authenticate, RequireIdempotency(dependencies.Idempotency, "unfollow_user", UserCallerScope))
		e.PUT("/api/v1/blocks/:userId", relationships.Block, authenticate, RequireIdempotency(dependencies.Idempotency, "block_user", UserCallerScope))
		e.DELETE("/api/v1/blocks/:userId", relationships.Unblock, authenticate, RequireIdempotency(dependencies.Idempotency, "unblock_user", UserCallerScope))
	}
	if dependencies.Administrators != nil {
		administrators := NewAdminUserHandler(dependencies.Administrators, dependencies.Logger)
		e.POST("/api/v1/admin/auth/login", administrators.Login, RequireIdempotency(dependencies.Idempotency, "admin_login", UnauthenticatedCallerScope(service.AuthAdminLogin)))
		authenticateAdmin := AdministratorAuthentication(dependencies.SessionStore)
		e.PUT("/api/v1/admin/users/:userId/certification", administrators.Certify, authenticateAdmin, RequireIdempotency(dependencies.Idempotency, "certify_user", AdministratorCallerScope))
		e.DELETE("/api/v1/admin/users/:userId/certification", administrators.RejectRemoval, authenticateAdmin, RequireIdempotency(dependencies.Idempotency, "remove_certification", AdministratorCallerScope))
	}
	if dependencies.Schedules != nil {
		handler := NewAdminScheduleHandler(dependencies.Schedules, dependencies.Logger)
		authenticate := AdministratorAuthentication(dependencies.SessionStore)
		e.PUT("/api/v1/admin/regions/:region/schedule", handler.Set, authenticate, RequireIdempotency(dependencies.Idempotency, "set_notification_schedule", AdministratorCallerScope))
		e.DELETE("/api/v1/admin/regions/:region/schedule", handler.Cancel, authenticate, RequireIdempotency(dependencies.Idempotency, "cancel_notification_schedule", AdministratorCallerScope))
	}
	if dependencies.Notifications != nil && dependencies.Cursor != nil {
		handler := NewNotificationHandler(dependencies.Notifications, dependencies.Cursor)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.GET("/api/v1/notifications", handler.List, authenticate)
		e.POST("/api/v1/notifications/:notificationId/open", handler.Open, authenticate, RequireIdempotency(dependencies.Idempotency, "open_notification", UserCallerScope))
	}
	if dependencies.PushSubscriptions != nil {
		handler := NewPushSubscriptionHandler(dependencies.PushSubscriptions)
		authenticate := UserAuthentication(dependencies.SessionStore)
		e.PUT("/api/v1/me/push-subscription", handler.Replace, authenticate, RequireIdempotency(dependencies.Idempotency, "replace_push_subscription", UserCallerScope))
		e.DELETE("/api/v1/me/push-subscription", handler.Remove, authenticate, RequireIdempotency(dependencies.Idempotency, "remove_push_subscription", UserCallerScope))
	}
	return e
}

func Readiness(ctx context.Context, db *postgres.DB) error {
	return db.Pool.Ping(ctx)
}
