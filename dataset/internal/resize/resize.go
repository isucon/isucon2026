// Package resize は YCbCr の画像から正方形や長方形を切り抜いて、大きさを変えた YCbCr 4:2:0 の
// 画像を作る。RGBA を経ないので、そのまま image/jpeg の速い経路で符号化できる。
package resize

import (
	"fmt"
	"image"
	"math"
)

// Crop は src の領域 (x, y, w, h)（輝度の画素）を width x height に拡大縮小する。mirror なら
// 左右を反転する。フィルタは縮小率に合わせて幅を広げた三角（縮小では面積平均に近い）。src は
// image/jpeg が返したもののように、原点から始まっていなければならない。
func Crop(src *image.YCbCr, x, y, w, h float64, width, height int, mirror bool) *image.YCbCr {
	sx, sy := chromaScale(src.SubsampleRatio)
	dst := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	b := src.Bounds()
	scaleX, scaleY := w/float64(width), h/float64(height)
	luma := func(off, scale float64, mirror bool, n int) func(int) float64 {
		return func(o int) float64 {
			if mirror {
				o = n - 1 - o
			}
			return off + (float64(o)+0.5)*scale
		}
	}
	// 4:2:0 の色差の画素は輝度の 2 x 2 の中心にあたる。
	chroma := func(off, scale float64, mirror bool, n, sub int) func(int) float64 {
		return func(o int) float64 {
			if mirror {
				o = n - 1 - o
			}
			return (off + float64(2*o+1)*scale) / float64(sub)
		}
	}
	cw, ch := (width+1)/2, (height+1)/2
	plane(dst.Y, dst.YStride, width, height, src.Y, src.YStride, b.Dx(), b.Dy(),
		newKernel(width, b.Dx(), scaleX, luma(x, scaleX, mirror, width)),
		newKernel(height, b.Dy(), scaleY, luma(y, scaleY, false, height)))
	srcCW, srcCH := (b.Dx()+sx-1)/sx, (b.Dy()+sy-1)/sy
	kx := newKernel(cw, srcCW, 2*scaleX/float64(sx), chroma(x, scaleX, mirror, cw, sx))
	ky := newKernel(ch, srcCH, 2*scaleY/float64(sy), chroma(y, scaleY, false, ch, sy))
	plane(dst.Cb, dst.CStride, cw, ch, src.Cb, src.CStride, srcCW, srcCH, kx, ky)
	plane(dst.Cr, dst.CStride, cw, ch, src.Cr, src.CStride, srcCW, srcCH, kx, ky)
	return dst
}

func chromaScale(ratio image.YCbCrSubsampleRatio) (int, int) {
	switch ratio {
	case image.YCbCrSubsampleRatio444:
		return 1, 1
	case image.YCbCrSubsampleRatio422:
		return 2, 1
	case image.YCbCrSubsampleRatio420:
		return 2, 2
	case image.YCbCrSubsampleRatio440:
		return 1, 2
	case image.YCbCrSubsampleRatio411:
		return 4, 1
	case image.YCbCrSubsampleRatio410:
		return 4, 2
	}
	panic(fmt.Sprintf("resize: unknown subsample ratio %v", ratio))
}

// 固定小数点の桁（ビット）。重みは weightBits、横に縮めた途中の値は画素の値に fractionBits の
// 端数を足して持つ。縦の積和は 255<<fractionBits<<weightBits で int32 に収まる。
const (
	weightBits   = 14
	fractionBits = 6
)

// kernel は出力の各画素について、入力の taps 個の位置と重みを持つ。
type kernel struct {
	taps    int
	index   []int32
	weights []int32
}

// newKernel は n 個の出力の画素の中心 center(o)（入力の座標）から重みを作る。scale は
// 出力 1 画素あたりの入力の画素数。範囲の外の位置は端の画素に寄せる。
func newKernel(n, size int, scale float64, center func(int) float64) kernel {
	support := max(1, scale)
	// 重みが 0 でない位置は幅 2*support の開区間に入る整数なので、最大でこの数。
	taps := int(math.Ceil(2 * support))
	k := kernel{taps: taps, index: make([]int32, n*taps), weights: make([]int32, n*taps)}
	ws := make([]float64, taps)
	for o := range n {
		c := center(o)
		first := int(math.Floor(c-support-0.5)) + 1
		var total float64
		for t := range taps {
			d := math.Abs(float64(first+t)+0.5-c) / support
			ws[t] = max(0, 1-d)
			total += ws[t]
		}
		var sum int32
		heaviest := 0
		for t := range taps {
			i := o*taps + t
			k.index[i] = int32(min(max(first+t, 0), size-1))
			k.weights[i] = int32(math.Round(ws[t] / total * (1 << weightBits)))
			sum += k.weights[i]
			if k.weights[i] > k.weights[o*taps+heaviest] {
				heaviest = t
			}
		}
		k.weights[o*taps+heaviest] += 1<<weightBits - sum
	}
	return k
}

// plane は 1 つの面を横、縦の順に拡大縮小する。
func plane(dst []byte, dstStride, width, height int, src []byte, srcStride, srcW, srcH int, kx, ky kernel) {
	// 縦の重みが参照する行だけを横に縮める。
	lo, hi := srcH, -1
	for _, i := range ky.index {
		lo, hi = min(lo, int(i)), max(hi, int(i))
	}
	rows := make([]int32, (hi-lo+1)*width)
	for r := lo; r <= hi; r++ {
		line := src[r*srcStride : r*srcStride+srcW]
		out := rows[(r-lo)*width : (r-lo+1)*width]
		for o := range out {
			var acc int32
			index, weights := kx.index[o*kx.taps:(o+1)*kx.taps], kx.weights[o*kx.taps:(o+1)*kx.taps]
			for t, i := range index {
				acc += int32(line[i]) * weights[t]
			}
			out[o] = (acc + 1<<(weightBits-fractionBits-1)) >> (weightBits - fractionBits)
		}
	}
	acc := make([]int32, width)
	const shift = weightBits + fractionBits
	for y := range height {
		clear(acc)
		for t := range ky.taps {
			w := ky.weights[y*ky.taps+t]
			if w == 0 {
				continue
			}
			row := rows[(int(ky.index[y*ky.taps+t])-lo)*width:][:width]
			for x, v := range row {
				acc[x] += v * w
			}
		}
		line := dst[y*dstStride:][:width]
		for x, v := range acc {
			line[x] = byte(min(max((v+1<<(shift-1))>>shift, 0), 255))
		}
	}
}
