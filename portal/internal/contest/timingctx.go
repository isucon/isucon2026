package contest

import (
	"context"

	"github.com/isucon/isucon2026/portal/internal/config"
)

type timingKey struct{}

// WithTiming はリクエスト単位の競技時刻設定（テストモードのヘッダ上書きを含む）を
// コンテキストに載せる。同期実行されるジョブがそのまま引き継ぐ。
func WithTiming(ctx context.Context, t config.Timing) context.Context {
	return context.WithValue(ctx, timingKey{}, t)
}

// TimingFromContext はコンテキストの設定を返す。無ければ fallback。
func TimingFromContext(ctx context.Context, fallback config.Timing) config.Timing {
	if t, ok := ctx.Value(timingKey{}).(config.Timing); ok {
		return t
	}
	return fallback
}
