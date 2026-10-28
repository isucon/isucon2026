// The scan encoder below follows Go's image/jpeg (writer.go and dct.go):
//
// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dataset

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"math/bits"
	"sync"
)

// renderJPEG は画像の中身（品質 80 の JPEG）を作る。bench は画像を確かめるたびに期待する
// 中身を作り直すので、ここが bench の CPU の大半を占めていた。画素は乱数で決まる絵を
// RGBA に描いて image/jpeg で符号化したものと同じバイト列を、途中の画像を作らずに書く。
// 大きさが 16 の倍数でなければ（MCU の端の扱いを省いたので）元のとおり描いて符号化する。
func renderJPEG(seed uint64, id string, size int) []byte {
	if size <= 0 || size%16 != 0 {
		return renderJPEGStandard(seed, id, size)
	}
	h := fnv.New64a()
	h.Write([]byte("image-" + id))
	// newRand(seed, "image-"+id) と同じ PCG を、状態をこの関数に置いて回す。
	src := pcg{hi: seed, lo: h.Sum64()}
	// newRand(...).IntN(256) と同じく、2 のべき乗は下位のビットを使う。
	baseR, baseG, baseB := uint8(src.next()&255), uint8(src.next()&255), uint8(src.next()&255)
	planes := renderPlanes.Get().(*[]uint8)
	need := 3 * size * size
	if cap(*planes) < need {
		*planes = make([]uint8, need)
	}
	ys, cbs, crs := (*planes)[:size*size], (*planes)[size*size:2*size*size], (*planes)[2*size*size:need]
	for y := range size {
		g := baseG + uint8(y*64/size)
		row := y * size
		for x := range size {
			r := int32((baseR + uint8(x*64/size)) ^ uint8(src.intN24()))
			gg := int32(g ^ uint8(src.intN24()))
			b := int32(baseB ^ uint8(src.intN24()))
			// color.RGBToYCbCr と同じ式。
			yy := (19595*r + 38470*gg + 7471*b + 1<<15) >> 16
			cb := -11056*r - 21712*gg + 32768*b + 257<<15
			if uint32(cb)&0xff000000 == 0 {
				cb >>= 16
			} else {
				cb = ^(cb >> 31)
			}
			cr := 32768*r - 27440*gg - 5328*b + 257<<15
			if uint32(cr)&0xff000000 == 0 {
				cr >>= 16
			} else {
				cr = ^(cr >> 31)
			}
			ys[row+x], cbs[row+x], crs[row+x] = uint8(yy), uint8(cb), uint8(cr)
		}
	}
	out := encodePlanes(ys, cbs, crs, size)
	renderPlanes.Put(planes)
	return out
}

var renderPlanes = sync.Pool{New: func() any { return new([]uint8) }}

// pcg は math/rand/v2 の PCG（rand.NewPCG(hi, lo)）と同じ乱数列を返す。
type pcg struct{ hi, lo uint64 }

// next は rand.PCG の Uint64。
func (p *pcg) next() uint64 {
	const (
		mulHi = 2549297995355413924
		mulLo = 4865540595714422341
		incHi = 6364136223846793005
		incLo = 1442695040888963407
	)
	hi, lo := bits.Mul64(p.lo, mulLo)
	hi += p.hi*mulLo + p.lo*mulHi
	lo, c := bits.Add64(lo, incLo, 0)
	hi, _ = bits.Add64(hi, incHi, c)
	p.lo, p.hi = lo, hi
	const cheapMul = 0xda942042e4dd58b5
	hi ^= hi >> 32
	hi *= cheapMul
	hi ^= hi >> 48
	hi *= lo | 1
	return hi
}

// intN24 は rand.Rand の IntN(24)（math/rand/v2 の uint64n）と同じ値を、同じ数だけ乱数を
// 使って返す。
func (p *pcg) intN24() uint64 {
	const n = 24
	hi, lo := bits.Mul64(p.next(), n)
	if lo < n {
		const thresh = (1<<64 - n) % n // -n % n in uint64
		for lo < thresh {
			hi, lo = bits.Mul64(p.next(), n)
		}
	}
	return hi
}

// renderJPEGStandard は絵を RGBA に描いて image/jpeg で符号化する（元の実装）。
func renderJPEGStandard(seed uint64, id string, size int) []byte {
	r := newRand(seed, "image-"+id)
	base := color.RGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255}
	picture := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			picture.SetRGBA(x, y, color.RGBA{
				R: base.R + uint8(x*64/size) ^ uint8(r.IntN(24)),
				G: base.G + uint8(y*64/size) ^ uint8(r.IntN(24)),
				B: base.B ^ uint8(r.IntN(24)),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: renderQuality}); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

const renderQuality = 80

// jpegTables は image/jpeg が品質 80 で書くヘッダーと、そこから読んだ量子化の除数と
// ハフマン符号。ヘッダーは画像の大きさだけで決まるので、大きさごとに image/jpeg で
// 一度だけ作る。
type jpegTables struct {
	header []byte
	// divisor と reciprocal は量子化の除数（8 × 量子化表、ジグザグ順）と、その割り算を
	// 掛け算にするための値（luminance、chrominance の順）。
	divisor    [2][64]uint64
	reciprocal [2][64]uint64
	half       [2][64]int32
	// huff は DC と AC の符号（luminance、chrominance の順）。上位 8 ビットが長さ。
	huff [4][256]uint32
}

var (
	tablesMu sync.Mutex
	tables   = map[int]*jpegTables{}
)

func tablesFor(size int) *jpegTables {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	if t := tables[size]; t != nil {
		return t
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, size, size)), &jpeg.Options{Quality: renderQuality}); err != nil {
		panic(err)
	}
	t := parseTables(buffer.Bytes())
	tables[size] = t
	return t
}

// parseTables はヘッダー（SOS の見出しまで）を切り出し、DQT と DHT を読む。
func parseTables(data []byte) *jpegTables {
	t := &jpegTables{}
	for i := 2; i+4 <= len(data); {
		marker, length := data[i+1], int(data[i+2])<<8|int(data[i+3])
		segment := data[i+4 : i+2+length]
		switch marker {
		case 0xdb: // DQT
			for k := 0; k < len(segment); k += 65 {
				q := segment[k] & 0x0f
				for zig := range 64 {
					d := 8 * uint64(segment[k+1+zig])
					t.divisor[q][zig] = d
					t.reciprocal[q][zig] = (1<<32 + d - 1) / d
					t.half[q][zig] = int32(d >> 1)
				}
			}
		case 0xc4: // DHT
			for k := 0; k < len(segment); {
				class, id := segment[k]>>4, segment[k]&0x0f
				counts := segment[k+1 : k+17]
				total := 0
				for _, c := range counts {
					total += int(c)
				}
				values := segment[k+17 : k+17+total]
				lut := &t.huff[2*int(id)+int(class)]
				code, v := uint32(0), 0
				for length, c := range counts {
					for range c {
						lut[values[v]] = uint32(length+1)<<24 | code
						code++
						v++
					}
					code <<= 1
				}
				k += 17 + total
			}
		case 0xda: // SOS: the header ends with its segment.
			t.header = bytes.Clone(data[:i+2+length])
			return t
		}
		i += 2 + length
	}
	panic("jpeg: no SOS marker")
}

// scanEncoder は image/jpeg の encoder のスキャン部分（writeBlock と emit）。
type scanEncoder struct {
	out         []byte
	bits, nBits uint32
	t           *jpegTables
}

// encodePlanes は Y、Cb、Cr の面（size × size、size は 16 の倍数）を image/jpeg と同じく
// 4:2:0 で符号化する。
func encodePlanes(ys, cbs, crs []uint8, size int) []byte {
	t := tablesFor(size)
	e := scanEncoder{t: t, out: make([]byte, 0, len(t.header)+size*size/2)}
	e.out = append(e.out, t.header...)
	var b block
	var prevY, prevCb, prevCr int32
	for my := 0; my < size; my += 16 {
		for mx := 0; mx < size; mx += 16 {
			for i := range 4 {
				ox, oy := mx+(i&1)*8, my+(i&2)*4
				for j := range 8 {
					row := ys[(oy+j)*size+ox : (oy+j)*size+ox+8]
					for k := range 8 {
						b[8*j+k] = int32(row[k])
					}
				}
				prevY = e.writeBlock(&b, 0, prevY)
			}
			subsample(&b, cbs, size, mx, my)
			prevCb = e.writeBlock(&b, 1, prevCb)
			subsample(&b, crs, size, mx, my)
			prevCr = e.writeBlock(&b, 1, prevCr)
		}
	}
	// Pad the last byte with 1's.
	e.emit(0x7f, 7)
	return append(e.out, 0xff, 0xd9)
}

// subsample は image/jpeg の scale と同じく、MCU の 2 × 2 画素の平均（切り上げ）を取る。
func subsample(dst *block, plane []uint8, size, mx, my int) {
	for y := range 8 {
		top := plane[(my+2*y)*size+mx : (my+2*y)*size+mx+16]
		bottom := plane[(my+2*y+1)*size+mx : (my+2*y+1)*size+mx+16]
		for x := range 8 {
			sum := int32(top[2*x]) + int32(top[2*x+1]) + int32(bottom[2*x]) + int32(bottom[2*x+1])
			dst[8*y+x] = (sum + 2) >> 2
		}
	}
}

// writeBlock は image/jpeg の writeBlock。量子化の div(a, d)（四捨五入の割り算）は、
// |a| < d/2 なら 0（d は 8 の倍数）で、それ以外は 32 ビットの逆数を掛けて求める（fdct の
// 出力は |a| < 2^13 なので割り算と同じ値になる）。
func (e *scanEncoder) writeBlock(b *block, q int, prevDC int32) int32 {
	fdct(b)
	divisor, reciprocal, half := &e.t.divisor[q], &e.t.reciprocal[q], &e.t.half[q]
	quantize := func(a int32, zig int) int32 {
		if a < half[zig] && a > -half[zig] {
			return 0
		}
		if a >= 0 {
			return int32(((uint64(a) + divisor[zig]>>1) * reciprocal[zig]) >> 32)
		}
		return -int32(((uint64(-a) + divisor[zig]>>1) * reciprocal[zig]) >> 32)
	}
	dc := quantize(b[0], 0)
	e.emitHuffRLE(2*q, 0, dc-prevDC)
	h, runLength := 2*q+1, int32(0)
	for zig := 1; zig < 64; zig++ {
		ac := quantize(b[unzig[zig]], zig)
		if ac == 0 {
			runLength++
			continue
		}
		for runLength > 15 {
			e.emitHuff(h, 0xf0)
			runLength -= 16
		}
		e.emitHuffRLE(h, runLength, ac)
		runLength = 0
	}
	if runLength > 0 {
		e.emitHuff(h, 0x00)
	}
	return dc
}

func (e *scanEncoder) emit(bits, nBits uint32) {
	nBits += e.nBits
	bits <<= 32 - nBits
	bits |= e.bits
	for nBits >= 8 {
		b := uint8(bits >> 24)
		e.out = append(e.out, b)
		if b == 0xff {
			e.out = append(e.out, 0x00)
		}
		bits <<= 8
		nBits -= 8
	}
	e.bits, e.nBits = bits, nBits
}

func (e *scanEncoder) emitHuff(h int, value int32) {
	x := e.t.huff[h][value]
	e.emit(x&(1<<24-1), x>>24)
}

func (e *scanEncoder) emitHuffRLE(h int, runLength, value int32) {
	a, b := value, value
	if a < 0 {
		a, b = -value, value-1
	}
	nBits := uint32(bits.Len32(uint32(a)))
	e.emitHuff(h, runLength<<4|int32(nBits))
	if nBits > 0 {
		e.emit(uint32(b)&(1<<nBits-1), nBits)
	}
}

type block [64]int32

// unzig maps from the zig-zag ordering to the natural ordering.
var unzig = [64]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

func dctBox(x0, x1, kcos, ksin int32) (y0, y1 int32) {
	ksum := kcos * (x0 + x1)
	y0 = ksum + (ksin-kcos)*x1
	y1 = ksum - (kcos+ksin)*x0
	return y0, y1
}

const (
	cos1       = 1130768441178740757 // fix cos 1*pi/16
	sin1       = 224923827593068887  // fix sin 1*pi/16
	cos3       = 958619196450722178  // fix cos 3*pi/16
	sin3       = 640528868967736374  // fix sin 3*pi/16
	sqrt2      = 1630477228166597777 // fix sqrt 2
	sqrt2_cos6 = 623956622067911264  // fix (sqrt 2)*cos 6*pi/16
	sqrt2_sin6 = 1506364539328854985 // fix (sqrt 2)*sin 6*pi/16
)

func c(x uint64, bits int) int32 {
	return int32((x + (1 << (59 - bits))) >> (60 - bits))
}

// fdct implements the forward DCT. Inputs are UQ8.0; outputs are Q13.0.
func fdct(b *block) {
	fdctCols(b)
	fdctRows(b)
}

func fdctCols(b *block) {
	for i := range 8 {
		x0 := b[0*8+i]
		x1 := b[1*8+i]
		x2 := b[2*8+i]
		x3 := b[3*8+i]
		x4 := b[4*8+i]
		x5 := b[5*8+i]
		x6 := b[6*8+i]
		x7 := b[7*8+i]
		x0, x7 = x0+x7, x0-x7
		x1, x6 = x1+x6, x1-x6
		x2, x5 = x2+x5, x2-x5
		x3, x4 = x3+x4, x3-x4
		x4, x7 = dctBox(x4, x7, c(cos3, 18), c(sin3, 18))
		x5, x6 = dctBox(x5, x6, c(cos1, 18), c(sin1, 18))
		x0, x3 = x0+x3, x0-x3
		x1, x2 = x1+x2, x1-x2
		x2, x3 = dctBox(x2, x3, c(sqrt2_cos6, 18), c(sqrt2_sin6, 18))
		x0, x1 = x0+x1, x0-x1
		b[0*8+i] = (x0 - 128*8) << 18
		b[4*8+i] = x1 << 18
		b[2*8+i] = x2
		b[6*8+i] = x3
		x4, x6 = x4+x6, x4-x6
		x7, x5 = x7+x5, x7-x5
		x5 = (x5 >> 12) * c(sqrt2, 12)
		x6 = (x6 >> 12) * c(sqrt2, 12)
		x7, x4 = x7+x4, x7-x4
		b[1*8+i] = x7
		b[3*8+i] = x5
		b[5*8+i] = x6
		b[7*8+i] = x4
	}
}

func fdctRows(b *block) {
	for i := range 8 {
		x := b[8*i : 8*i+8 : 8*i+8]
		x0 := x[0]
		x1 := x[1]
		x2 := x[2]
		x3 := x[3]
		x4 := x[4]
		x5 := x[5]
		x6 := x[6]
		x7 := x[7]
		x0, x7 = x0+x7, x0-x7
		x1, x6 = x1+x6, x1-x6
		x2, x5 = x2+x5, x2-x5
		x3, x4 = x3+x4, x3-x4
		x4, x7 = dctBox(x4>>14, x7>>14, c(cos3, 14), c(sin3, 14))
		x5, x6 = dctBox(x5>>14, x6>>14, c(cos1, 14), c(sin1, 14))
		x0, x3 = x0+x3, x0-x3
		x1, x2 = x1+x2, x1-x2
		x2, x3 = dctBox(x2>>14, x3>>14, c(sqrt2_cos6, 14), c(sqrt2_sin6, 14))
		x0, x1 = x0+x1, x0-x1
		x4, x6 = x4+x6, x4-x6
		x7, x5 = x7+x5, x7-x5
		x5 = (x5 >> 14) * c(sqrt2, 14)
		x6 = (x6 >> 14) * c(sqrt2, 14)
		x7, x4 = x7+x4, x7-x4
		x0 = (x0 + 1<<17) >> 18
		x1 = (x1 + 1<<17) >> 18
		x2 = (x2 + 1<<17) >> 18
		x3 = (x3 + 1<<17) >> 18
		x4 = (x4 + 1<<17) >> 18
		x5 = (x5 + 1<<17) >> 18
		x6 = (x6 + 1<<17) >> 18
		x7 = (x7 + 1<<17) >> 18
		x[0] = x0
		x[1] = x7
		x[2] = x2
		x[3] = x5
		x[4] = x1
		x[5] = x6
		x[6] = x3
		x[7] = x4
	}
}
