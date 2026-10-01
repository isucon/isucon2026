package job

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: 5 * time.Second}, // 不正値は 1 回目扱い
		{attempts: 1, want: 5 * time.Second},
		{attempts: 2, want: 10 * time.Second},
		{attempts: 3, want: 20 * time.Second},
		{attempts: 6, want: 160 * time.Second},
		{attempts: 7, want: 5 * time.Minute},  // 上限で頭打ち
		{attempts: 60, want: 5 * time.Minute}, // シフトのオーバーフローも上限に丸める
	}
	for _, tt := range tests {
		if got := backoff(tt.attempts); got != tt.want {
			t.Errorf("backoff(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}

func TestBackoffNeverExceedsLockTTL(t *testing.T) {
	// 再試行までの待ち時間はロック TTL より短い（dead 判定の前にロックが切れないと再配信が滞る）。
	for attempts := 1; attempts <= dbQueueMaxAttempts; attempts++ {
		if got := backoff(attempts); got >= dbQueueLockTTL {
			t.Errorf("backoff(%d) = %v must be shorter than lock TTL %v", attempts, got, dbQueueLockTTL)
		}
	}
}
