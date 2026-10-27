package photo

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 投稿ごとに画素まで異なる写真にする。
//
// COM セグメントの印だけでは、印を除いた残り（画素のデータ）が同じ写真を投稿した人どうしで
// 一致する。アプリが写真の中身で重複を除けば、保存や DB への転送を大きく省けてしまう。実際の
// 写真は 1 枚ずつ画素が違うので、baseline JPEG のエントロピー符号化データで、AC 係数の付加
// ビットの最下位を投稿ごとに選んで反転する。付加ビットが 2 ビット以上の係数だけを選ぶので、
// 係数は同じ大きさの区分の中で ±1 しか変わらず、ハフマン符号もデータの長さも変わらない
// （再エンコードしない）。反転はデータの windowSize バイトごとに 1 か所置くので、数 KB 単位の
// ブロックの一致でも重複を除けない。

// windowSize は反転を 1 か所置くデータの幅（バイト）。
const windowSize = 256

// maxCandidates は 1 つの幅で覚えておく、反転できるビットの数。
const maxCandidates = 8

// flip は反転できるビット。off は JPEG の先頭からのバイト位置、mask はそのバイトの中のビット。
type flip struct {
	off  uint32
	mask byte
}

// flips は windowSize バイトごとの、反転できるビットの候補。
type flips [][]flip

// apply は data[from:] を out に足す。key で幅ごとに 1 か所ずつ選んだビットを反転し、
// バイトスタッフィング（データの 0xFF の後に置く 0x00）を合わせて直す。
func (f flips) apply(out, data []byte, from int, key uint64) []byte {
	prev := from
	for window, candidates := range f {
		if len(candidates) == 0 {
			continue
		}
		c := candidates[mix(key, uint64(window))%uint64(len(candidates))]
		off := int(c.off)
		out = append(out, data[prev:off]...)
		b := data[off] ^ c.mask
		out = append(out, b)
		prev = off + 1
		if data[off] == 0xFF {
			prev++ // 元のスタッフィングの 0x00
		}
		if b == 0xFF {
			out = append(out, 0x00)
		}
	}
	return append(out, data[prev:]...)
}

// mix は key と幅の番号から候補を選ぶための値を作る（splitmix64）。
func mix(key, window uint64) uint64 {
	z := key ^ (window+1)*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

type component struct {
	id, h, v byte
}

type huffman struct {
	maxcode [18]int32
	valptr  [17]int32
	mincode [17]int32
	values  []byte
}

func newHuffman(counts []byte, values []byte) (*huffman, error) {
	h := &huffman{values: values}
	code, k := int32(0), int32(0)
	for l := 1; l <= 16; l++ {
		n := int32(counts[l-1])
		h.valptr[l] = k
		h.mincode[l] = code
		code += n
		k += n
		h.maxcode[l] = code - 1
		if n == 0 {
			h.maxcode[l] = -1
		}
		code <<= 1
	}
	h.maxcode[17] = 1 << 30
	if int(k) > len(values) {
		return nil, errors.New("ハフマン表の値が足りません")
	}
	return h, nil
}

// bitReader はスタッフィングを外しながらスキャンを読む。
type bitReader struct {
	data []byte
	pos  int // 次に読むバイトの位置
	off  int // cur の位置
	cur  byte
	left uint
}

var errScanEnd = errors.New("スキャンのデータが途中で終わっています")

func (r *bitReader) bit() (int32, error) {
	if r.left == 0 {
		if r.pos >= len(r.data) {
			return 0, errScanEnd
		}
		b := r.data[r.pos]
		r.off = r.pos
		r.pos++
		if b == 0xFF {
			if r.pos >= len(r.data) || r.data[r.pos] != 0x00 {
				return 0, errScanEnd
			}
			r.pos++
		}
		r.cur, r.left = b, 8
	}
	r.left--
	return int32(r.cur>>r.left) & 1, nil
}

func (r *bitReader) skip(n byte) error {
	for range n {
		if _, err := r.bit(); err != nil {
			return err
		}
	}
	return nil
}

func (r *bitReader) decode(h *huffman) (byte, error) {
	code, err := r.bit()
	if err != nil {
		return 0, err
	}
	l := 1
	for code > h.maxcode[l] {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		code = code<<1 | b
		l++
		if l > 16 {
			return 0, errors.New("ハフマン符号が壊れています")
		}
	}
	j := h.valptr[l] + code - h.mincode[l]
	if int(j) >= len(h.values) {
		return 0, errors.New("ハフマン符号が壊れています")
	}
	return h.values[j], nil
}

// findFlips は baseline JPEG の最初のスキャンを復号し、反転できるビットを集める。
// プログレッシブや算術符号、リスタートマーカーを使う JPEG は対象にせず nil を返す
// （COM セグメントの印だけになる）。
func findFlips(data []byte) (flips, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errors.New("JPEG ではありません")
	}
	var (
		frame        []component
		width        int
		height       int
		dc, ac       [4]*huffman
		baselineSeen bool
	)
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return nil, errors.New("JPEG のセグメントが壊れています")
		}
		marker := data[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0xD9 {
			return nil, nil
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		end := i + 2 + length
		if length < 2 || end > len(data) {
			return nil, errors.New("JPEG のセグメントが壊れています")
		}
		segment := data[i+4 : end]
		switch {
		case marker == 0xC0 || marker == 0xC1:
			if len(segment) < 6 || segment[0] != 8 {
				return nil, nil
			}
			height = int(binary.BigEndian.Uint16(segment[1:]))
			width = int(binary.BigEndian.Uint16(segment[3:]))
			n := int(segment[5])
			if len(segment) < 6+3*n || n == 0 || width == 0 || height == 0 {
				return nil, errors.New("SOF が壊れています")
			}
			for k := range n {
				c := segment[6+3*k:]
				frame = append(frame, component{id: c[0], h: c[1] >> 4, v: c[1] & 15})
			}
			baselineSeen = true
		case marker >= 0xC2 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC:
			return nil, nil // プログレッシブ、可逆、算術符号
		case marker == 0xC4:
			for p := 0; p < len(segment); {
				if p+17 > len(segment) {
					return nil, errors.New("DHT が壊れています")
				}
				class, slot := segment[p]>>4, segment[p]&15
				counts := segment[p+1 : p+17]
				total := 0
				for _, c := range counts {
					total += int(c)
				}
				if slot > 3 || p+17+total > len(segment) {
					return nil, errors.New("DHT が壊れています")
				}
				h, err := newHuffman(counts, segment[p+17:p+17+total])
				if err != nil {
					return nil, err
				}
				if class == 0 {
					dc[slot] = h
				} else {
					ac[slot] = h
				}
				p += 17 + total
			}
		case marker == 0xDD:
			if len(segment) >= 2 && binary.BigEndian.Uint16(segment) != 0 {
				return nil, nil // リスタートマーカー
			}
		case marker == 0xDA:
			if !baselineSeen {
				return nil, nil
			}
			return decodeScan(data, end, segment, frame, width, height, dc, ac)
		}
		i = end
	}
	return nil, errors.New("JPEG にスキャンがありません")
}

func decodeScan(data []byte, start int, header []byte, frame []component, width, height int, dc, ac [4]*huffman) (flips, error) {
	if len(header) == 0 {
		return nil, errors.New("SOS が壊れています")
	}
	n := int(header[0])
	if n == 0 || len(header) < 1+2*n+3 {
		return nil, errors.New("SOS が壊れています")
	}
	type scanComponent struct {
		component
		dc, ac *huffman
	}
	var hmax, vmax byte = 1, 1
	for _, c := range frame {
		hmax, vmax = max(hmax, c.h), max(vmax, c.v)
	}
	var scan []scanComponent
	for k := range n {
		id, tables := header[1+2*k], header[2+2*k]
		var found *component
		for f := range frame {
			if frame[f].id == id {
				found = &frame[f]
			}
		}
		if found == nil || tables>>4 > 3 || tables&15 > 3 || dc[tables>>4] == nil || ac[tables&15] == nil {
			return nil, errors.New("SOS の成分か表が見つかりません")
		}
		scan = append(scan, scanComponent{component: *found, dc: dc[tables>>4], ac: ac[tables&15]})
	}
	ceilDiv := func(a, b int) int { return (a + b - 1) / b }
	var mcus, blocksPerMCU int
	if n == 1 {
		// 1 成分だけのスキャンは MCU が 1 ブロックで、成分の大きさだけ並ぶ。
		c := scan[0]
		w := ceilDiv(width*int(c.h), int(hmax))
		h := ceilDiv(height*int(c.v), int(vmax))
		mcus = ceilDiv(w, 8) * ceilDiv(h, 8)
		scan[0].h, scan[0].v = 1, 1
	} else {
		mcus = ceilDiv(width, 8*int(hmax)) * ceilDiv(height, 8*int(vmax))
	}
	for _, c := range scan {
		blocksPerMCU += int(c.h) * int(c.v)
	}
	result := make(flips, len(data)/windowSize+1)
	r := &bitReader{data: data, pos: start}
	for range mcus {
		for _, c := range scan {
			for range int(c.h) * int(c.v) {
				size, err := r.decode(c.dc)
				if err != nil {
					return nil, err
				}
				if err := r.skip(size); err != nil {
					return nil, err
				}
				for k := 1; k < 64; {
					rs, err := r.decode(c.ac)
					if err != nil {
						return nil, err
					}
					run, size := int(rs>>4), rs&15
					if size == 0 {
						if run != 15 {
							break
						}
						k += 16
						continue
					}
					k += run + 1
					if err := r.skip(size); err != nil {
						return nil, err
					}
					if size >= 2 {
						window := r.off / windowSize
						if len(result[window]) < maxCandidates {
							result[window] = append(result[window], flip{off: uint32(r.off), mask: 1 << r.left})
						}
					}
				}
			}
		}
	}
	if blocksPerMCU == 0 {
		return nil, fmt.Errorf("スキャンにブロックがありません")
	}
	return result, nil
}
