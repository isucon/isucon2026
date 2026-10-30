package dataset

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand/v2"

	"github.com/isucon/isucon2026/dataset/internal/resize"
)

// noveltyRate は投稿の写真が変わり種（椅子のダジャレの写真）になる割合。bench の投稿と同じ。
const noveltyRate = 0.05

// imageQuality は画像の JPEG の品質。履歴の大きさが写真にする前のノイズの画像とほぼ同じに
// なるように選んでいる（Large で投稿が約 15 KB、RealMoji が約 3.5 KB）。
const imageQuality = 75

// ImageBytes は画像の中身（JPEG）を返す。椅子の写真から ID で決まる位置を切り抜き、明るさと
// 色味を少しずらして、画素ごとに小さな揺らぎを足す。中身は ID で決まり、画像ごとに違う（同じ
// 中身の画像をまとめても小さくならない）。bench は画像の行から期待する中身を計算できる。
func (w *World) ImageBytes(img *Image) []byte {
	size, zoom := w.Config.PostImageSize, 0.8
	switch img.Kind {
	case ImageRealMoji:
		size, zoom = w.Config.RealMojiImageSize, 0.5
	case ImageProfile:
		size, zoom = w.Config.ProfileImageSize, 0.5
	}
	photo, r := w.imagePhoto(img)
	return renderPhoto(photo.decoded(), r, size, zoom)
}

// imagePhoto は画像の元にする写真と、切り抜きなどを選ぶ乱数列を返す。投稿の写真は投稿者の
// リージョンの写真から選ぶ。RealMoji とプロフィールの画像には変わり種を使わない。
func (w *World) imagePhoto(img *Image) (*chair, *rand.Rand) {
	r := newRand(w.Config.Seed, "image-"+img.ID)
	if img.Kind != ImagePost {
		return choose(r, plainChairs), r
	}
	candidates := plainChairs
	if i, ok := w.userIndex[img.OwnerID]; ok {
		candidates = regionChairs[w.Users[i].Region]
	}
	if r.Float64() < noveltyRate {
		candidates = noveltyChairs
	}
	return choose(r, candidates), r
}

// renderPhoto は src から短い辺の zoom 倍以上の正方形を切り抜き、size x size の JPEG にする。
func renderPhoto(src *image.YCbCr, r *rand.Rand, size int, zoom float64) []byte {
	b := src.Bounds()
	side := float64(min(b.Dx(), b.Dy())) * (zoom + (1-zoom)*r.Float64())
	// 椅子は写真の中央にあることが多いので、切り抜く位置を中央に寄せる（三角分布）。
	x := (float64(b.Dx()) - side) * (1 + r.Float64() - r.Float64()) / 2
	y := (float64(b.Dy()) - side) * (1 + r.Float64() - r.Float64()) / 2
	return finishPhoto(resize.Crop(src, x, y, side, side, size, size, r.IntN(2) == 0), r)
}

// finishPhoto は撮った端末や光の違いとして明るさと色味を少しずらし、JPEG にする。輝度には
// 画素ごとに -1〜+1 の揺らぎを足し、同じ写真の同じ位置を切り抜いても中身が一致しないようにする。
func finishPhoto(picture *image.YCbCr, r *rand.Rand) []byte {
	brightness, blue, red := r.IntN(17)-8, r.IntN(7)-3, r.IntN(7)-3
	width, height := picture.Rect.Dx(), picture.Rect.Dy()
	var bits uint64
	for py := range height {
		row := picture.Y[py*picture.YStride : py*picture.YStride+width]
		for px := range row {
			if px%32 == 0 {
				bits = r.Uint64()
			}
			noise := int(bits&1) - int(bits>>1&1)
			bits >>= 2
			row[px] = clamp(int(row[px]) + brightness + noise)
		}
	}
	for i := range picture.Cb {
		picture.Cb[i] = clamp(int(picture.Cb[i]) + blue)
		picture.Cr[i] = clamp(int(picture.Cr[i]) + red)
	}

	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: imageQuality}); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func clamp(v int) byte {
	return byte(min(max(v, 0), 255))
}
