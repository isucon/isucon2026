package dataset

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// derivedID は fmt で組み立てていた元の実装と同じ ID を返す。
func TestDerivedIDMatchesFormat(t *testing.T) {
	t.Parallel()
	want := func(seed uint64, kind string, parts ...int) string {
		h := sha256.New()
		fmt.Fprintf(h, "%d:%s", seed, kind)
		for _, part := range parts {
			fmt.Fprintf(h, ":%d", part)
		}
		b := h.Sum(nil)
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	for _, seed := range []uint64{0, 1, 20261028, 1<<64 - 1} {
		for _, kind := range []string{"post", "realmoji-image", "notification", ""} {
			for _, parts := range [][]int{nil, {0}, {364, 4999}, {-1, 12, 123456789}} {
				if got, w := derivedID(seed, kind, parts...), want(seed, kind, parts...); got != w {
					t.Errorf("derivedID(%d, %q, %v) = %s, want %s", seed, kind, parts, got, w)
				}
			}
		}
	}
}
