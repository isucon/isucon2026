package imageurl

import (
	"fmt"
	"testing"
	"time"
)

func TestSignatureCache(t *testing.T) {
	c := &signatureCache{limit: 2}
	for i := range 5 {
		for range 2 {
			if got, want := c.sign("secret", fmt.Sprint("img", i), 100), Sign("secret", fmt.Sprint("img", i), 100); got != want {
				t.Fatalf("img%d: got %s, want %s", i, got, want)
			}
		}
		if len(c.current) > c.limit || len(c.previous) > c.limit {
			t.Fatalf("cache exceeds the limit: %d + %d", len(c.current), len(c.previous))
		}
	}
	// 鍵、画像、期限のどれかが違えば別の署名になる。
	base := c.sign("secret", "img", 100)
	for _, other := range []string{c.sign("other", "img", 100), c.sign("secret", "img2", 100), c.sign("secret", "img", 101)} {
		if other == base {
			t.Fatal("different inputs share a signature")
		}
	}
}

func BenchmarkVerify(b *testing.B) {
	exp := int64(1 << 40)
	url := fmt.Sprintf("/api/images/img?exp=%d&sig=%s", exp, Sign("secret", "img", exp))
	b.Run("uncached", func(b *testing.B) {
		for b.Loop() {
			Sign("secret", "img", exp)
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := Verify(url, "img", "secret", timeBefore(exp)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func timeBefore(exp int64) time.Time { return time.Unix(exp, 0).Add(-time.Hour) }
