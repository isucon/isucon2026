// cmd/worker は SQS コンシューマ（worker タスク相当）。ジョブ 20 本を処理する。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer a.DB.Close()

	if a.Cfg.SQSQueueURL == "" || a.AWS == nil {
		return errors.New("ISUXPORTAL_SQS_QUEUE_URL and AWS credentials are required for worker")
	}

	slog.Info("worker started", "queue", a.Cfg.SQSQueueURL)
	for {
		select {
		case <-ctx.Done():
			slog.Info("worker shutting down")
			return nil
		default:
		}
		msgs, err := a.AWS.ReceiveSQSMessages(ctx, a.Cfg.SQSQueueURL)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("sqs receive failed", "error", err)
			continue
		}
		for _, m := range msgs {
			var payload job.Payload
			if err := json.Unmarshal([]byte(m.Body), &payload); err != nil {
				slog.Error("invalid job payload", "error", err, "body", m.Body)
				_ = a.AWS.DeleteSQSMessage(ctx, a.Cfg.SQSQueueURL, m.ReceiptHandle)
				continue
			}
			if err := a.Jobs.Perform(ctx, payload.Name, payload.Args); err != nil {
				// 失敗したメッセージは削除せず SQS の再配信に任せる
				slog.Error("job failed", "job", payload.Name, "error", err)
				continue
			}
			_ = a.AWS.DeleteSQSMessage(ctx, a.Cfg.SQSQueueURL, m.ReceiptHandle)
		}
	}
}
