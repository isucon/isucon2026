package dataset

import (
	"bytes"
	"strconv"
	"sync"
	"testing"
)

// renderJPEG は image/jpeg で描いた元の実装と同じバイト列を返す（初期データの画像は元の
// 実装で作ったものなので、1 バイトでも違えば bench はすべての画像を誤りとみなす）。
func TestRenderJPEGMatchesStandard(t *testing.T) {
	t.Parallel()
	sizes := []int{320, 128, 96, 64, 48, 32, 16, 40, 1}
	n := 200
	if testing.Short() {
		n = 20
	}
	var wg sync.WaitGroup
	for _, size := range sizes {
		for _, seed := range []uint64{1, 20261028} {
			wg.Go(func() {
				for i := range n {
					id := "0190a1b2-c3d4-7e5f-8a9b-" + strconv.Itoa(100000000000+i*7919)
					got, want := renderJPEG(seed, id, size), renderJPEGStandard(seed, id, size)
					if !bytes.Equal(got, want) {
						t.Errorf("size %d seed %d id %s: %d bytes, want %d bytes", size, seed, id, len(got), len(want))
						return
					}
				}
			})
		}
	}
	wg.Wait()
}

func BenchmarkRenderJPEG(b *testing.B) {
	for _, size := range []int{320, 128, 96} {
		for _, impl := range []struct {
			name   string
			render func(uint64, string, int) []byte
		}{{"fast", renderJPEG}, {"standard", renderJPEGStandard}} {
			b.Run(impl.name+"/"+strconv.Itoa(size), func(b *testing.B) {
				b.ReportAllocs()
				for i := range b.N {
					impl.render(1, "00000000-0000-4000-8000-"+strconv.Itoa(100000000000+i), size)
				}
			})
		}
	}
}
