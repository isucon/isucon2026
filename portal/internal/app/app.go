// Package app は各 cmd が共有する依存の初期化を担う。
package app

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/isucon/isucon2026/portal/internal/cache"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/session"
)

type App struct {
	Cfg   *config.Config
	Store *session.Store
	Cache *cache.Cache
	Jobs  *job.Env
	Push  *external.WebPush
	AWS   *external.AWS
	DB    *sql.DB
}

// New は設定・DB・Redis・外部クライアント・ジョブ環境を組み立てる。
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	conn, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := conn.PingContext(ctx); err != nil {
		return nil, err
	}

	store, err := session.NewStore(cfg.RedisURL, cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	if err := store.Ping(ctx); err != nil {
		return nil, err
	}

	pem, err := cfg.VAPIDPrivateKeyPEM()
	if err != nil {
		return nil, err
	}
	push, err := external.NewWebPush(pem, cfg.VAPIDSubject)
	if err != nil {
		return nil, err
	}

	var aws *external.AWS
	if a, err := external.NewAWS(ctx, cfg.AWSRegion); err != nil {
		slog.Warn("aws client unavailable", "error", err)
	} else {
		aws = a
	}

	jobs := &job.Env{
		DB:    conn,
		Cfg:   cfg,
		Cache: cache.New(store.Redis()),
		Slack: &external.Slack{WebhookURL: cfg.SlackWebhookURL},
		Discord: &external.Discord{
			BotToken:   cfg.Discord.BotToken,
			APIBaseURL: cfg.Discord.APIBaseURL,
			ServerID:   cfg.Discord.ServerID,
		},
		GitHub: &external.GitHub{APIBaseURL: cfg.GitHub.APIBaseURL},
		Push:   push,
		AWS:    aws,
		SSH:    &external.SSHExecutor{User: "isuadmin", PrivateKeyPath: cfg.IsuconAdminSSHKeyPath},
	}

	// キュー選択: テストモードは同期実行（perform_now 相当）、それ以外は
	// PostgreSQL の jobs テーブル（cmd/worker が消費）。
	if cfg.TestMode {
		jobs.SetQueue(&job.InProcessQueue{Env: jobs, Sync: true})
	} else {
		jobs.SetQueue(&job.DBQueue{DB: conn})
	}

	return &App{
		Cfg:   cfg,
		Store: store,
		Cache: jobs.Cache,
		Jobs:  jobs,
		Push:  push,
		AWS:   aws,
		DB:    conn,
	}, nil
}
