package isureal

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type HandlerContext struct {
	DB *sqlx.DB
	// AdminDB is a small separate pool for the administrator's operations.
	// Opening a posting window must not queue behind hundreds of user writes
	// waiting for DB (the bench gives the opening 8 s of a 9 s day); nil
	// means DB.
	AdminDB *sqlx.DB
	Config  *Config
	IPBans  *IPBanList
}

func (h *HandlerContext) adminDB() *sqlx.DB {
	if h.AdminDB != nil {
		return h.AdminDB
	}
	return h.DB
}

func NewServer(config *Config) *echo.Echo {
	if !validRegion(config.Region) {
		panic("invalid server region: " + config.Region)
	}
	homeRegion = config.Region
	e := echo.New()
	e.IPExtractor = extractClientIP

	// Logging every request costs CPU on the hot path; enable it to debug.
	if os.Getenv("ISUREAL_ACCESS_LOG") != "" {
		e.Use(middleware.RequestLogger())
	}
	e.Use(middleware.Recover())
	e.HTTPErrorHandler = APIErrorHandler

	db, err := sqlx.Open("pgx-auth", config.DatabaseDSN())
	if err != nil {
		panic("failed to open database: " + err.Error())
	}
	// Bound concurrency per app VM and retain connections between requests.
	// Apps far from the database are bound by round trips rather than CPU, so
	// they need more connections in flight (ISUREAL_DB_MAX_CONNS).
	maxConns := 32
	if n, err := strconv.Atoi(os.Getenv("ISUREAL_DB_MAX_CONNS")); err == nil && n > 0 {
		maxConns = n
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	err = db.Ping()
	if err != nil {
		panic("failed to ping database: " + err.Error())
	}

	if dir := os.Getenv("ISUREAL_IMAGE_DIR"); dir != "" {
		if p, err := openImagePack(context.Background(), db, dir); err != nil {
			slog.Warn("image pack not used", "error", err)
		} else {
			pack = p
			slog.Info("image pack", "images", len(p.entries))
		}
	}

	adminDB, err := sqlx.Open("pgx-auth", config.DatabaseDSN())
	if err != nil {
		panic("failed to open database: " + err.Error())
	}
	adminDB.SetMaxOpenConns(4)
	adminDB.SetMaxIdleConns(4)

	handlerContext := &HandlerContext{
		DB:      db,
		AdminDB: adminDB,
		Config:  config,
		IPBans:  &IPBanList{},
	}
	// Load once before serving so bans apply from the first request.
	_ = handlerContext.IPBans.reload(context.Background(), db)
	go handlerContext.IPBans.run(context.Background(), db)
	e.Use(IPBanMiddleware(handlerContext))

	registerSyncHandlers(e, handlerContext)
	// ISUREAL_HOT_STATE=off keeps the regional state unloaded, so every read
	// goes to the database (for tests that edit the database directly).
	go func() {
		for {
			if err := hot.load(context.Background(), db); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if os.Getenv("ISUREAL_HOT_STATE") == "off" {
			return
		}
		for {
			err := history.load(context.Background(), db, config.Region)
			if err == nil {
				return
			}
			slog.Error("history store", "error", err)
			time.Sleep(time.Second)
		}
	}()

	e.POST("/api/initialize", initializeHandler(handlerContext))
	e.POST("/api/auth/register", registerHandler(handlerContext))
	e.POST("/api/auth/login", loginHandler(handlerContext))
	e.POST("/api/auth/logout", logoutHandler(handlerContext), BatchSessionMiddleware(handlerContext, true))
	e.GET("/api/users", searchUsersHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/users/:userId", getUserHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/me/export", exportPostsHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/me", getMeHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PATCH("/api/me", updateMeHandler(handlerContext), BatchSessionMiddleware(handlerContext, true))
	e.PUT("/api/me/profile-image", putProfileImageHandler(handlerContext), BatchSessionMiddleware(handlerContext, true))
	e.DELETE("/api/me/profile-image", deleteProfileImageHandler(handlerContext), BatchSessionMiddleware(handlerContext, true))
	e.POST("/api/me/account-deletion", deleteAccountHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts", createPostHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posting-window/current", getCurrentPostingWindowHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/me/current", getMyCurrentPostHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId", getPostHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PATCH("/api/posts/:postId", updatePostHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/posts/:postId", deletePostHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/feeds/:feed", getFeedHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/memories", getMemoriesHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/memories/:date", getMemoriesByDateHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts/:postId/comments", createCommentHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId/comments", getCommentsHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/comments/:commentId", deleteCommentHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts/:postId/realmojis", replaceRealMojiHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId/realmojis", getRealMojisHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/realmojis/:realMojiId", deleteRealMojiHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/images/:imageId", getImageHandler(handlerContext))
	e.GET("/api/notifications", getNotificationsHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/friend-requests", createFriendRequestHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/friend-requests/:requestId/:action", transitionFriendRequestHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/relationships/:kind", getRelationshipsHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/friendships/:userId", deleteFriendshipHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PUT("/api/follows/:userId", followHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/follows/:userId", unfollowHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PUT("/api/blocks/:userId", blockHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/blocks/:userId", unblockHandler(handlerContext), BatchSessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/admin/auth/login", adminLoginHandler(handlerContext))
	e.POST("/api/admin/auth/logout", adminLogoutHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.PUT("/api/admin/users/:userId/certification", certifyUserHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.DELETE("/api/admin/users/:userId/certification", deleteUserCertificationHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.POST("/api/admin/posting-window", openPostingWindowHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))

	return e
}
