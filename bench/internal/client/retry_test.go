package client

import (
	"testing"
	"time"
)

// TestRetryAfterLargeSeconds は、time.Duration に直すとあふれる大きな Retry-After の秒数でも、
// 待たずに再試行せず MaxWait まで待つことを確かめる。
func TestRetryAfterLargeSeconds(t *testing.T) {
	p := RetryPolicy{MaxRetries: 1, Base: time.Millisecond, MaxWait: 2 * time.Second}
	for _, tc := range []struct {
		retryAfter string
		want       time.Duration
	}{
		{"0", 0},
		{"1", time.Second},
		{"2", 2 * time.Second},
		{"30", 2 * time.Second},
		{"10000000000", 2 * time.Second},
		{"9223372036854775807", 2 * time.Second},
		{"99999999999999999999999999", 2 * time.Second},
	} {
		if got := p.wait(0, tc.retryAfter); got != tc.want {
			t.Errorf("Retry-After %q: wait %s, want %s", tc.retryAfter, got, tc.want)
		}
	}
	// 秒数の形でない値（負の数を含む）は無視して指数バックオフで待つ。
	for _, retryAfter := range []string{"-1", "+1", "1.5", "soon"} {
		if got := p.wait(0, retryAfter); got < 0 || got > p.Base {
			t.Errorf("Retry-After %q: wait %s, want the backoff (at most %s)", retryAfter, got, p.Base)
		}
	}
}
