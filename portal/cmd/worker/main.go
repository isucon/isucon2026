// cmd/worker は PostgreSQL の jobs テーブルを消費するワーカー（worker タスク相当）。ジョブ 20 本を処理する。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/isucon/isucon2026/portal/internal/app"
	"github.com/isucon/isucon2026/portal/internal/job"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	syncProject := flag.String("sync-instances", "", "Synchronize tagged EC2 instances for this project once, then exit")
	trialTeams := flag.String("create-trial-teams", "", "Comma-separated trial team IDs to create before one-shot sync")
	flag.Parse()
	if *trialTeams != "" && *syncProject == "" {
		return fmt.Errorf("create-trial-teams requires sync-instances")
	}
	ids := []int64{}
	if *trialTeams != "" {
		for _, value := range strings.Split(*trialTeams, ",") {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid trial team ID %q", value)
			}
			ids = append(ids, id)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer a.DB.Close()

	if *syncProject != "" {
		if err := a.Jobs.EnsureTrialTeams(ctx, ids); err != nil {
			return err
		}
		if err := a.Jobs.SyncContestantInstances(ctx, *syncProject); err != nil {
			return err
		}
		slog.Info("EC2 instance synchronization completed", "project", *syncProject)
		return nil
	}
	queue := &job.DBQueue{DB: a.DB}
	slog.Info("worker started", "poll_interval", job.DefaultPollInterval)
	queue.Run(ctx, a.Jobs, job.DefaultPollInterval)
	slog.Info("worker shutting down")
	return nil
}
