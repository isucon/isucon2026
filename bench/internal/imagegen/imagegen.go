// Package imagegen は負荷生成用の決定的な JPEG を作る。アプリは JPEG
// （先頭が FF D8 で Go の image/jpeg でデコードできるもの）しか受け付けないので、
// 同じ標準ライブラリでエンコードする。
package imagegen

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
)

const encodeQuality = 80

// JPEG は width x height のノイズ入りグラデーション画像を作る。同じ seed なら
// 常に同じバイト列になるので、走行を再現できる（CAL-01）。
func JPEG(width, height int, seed uint64) ([]byte, error) {
	random := rand.New(rand.NewPCG(seed, seed))
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.SetRGBA(x, y, color.RGBA{
				R: uint8((x * 255 / width) ^ int(random.Uint64()&0x1F)),
				G: uint8((y * 255 / height) ^ int(random.Uint64()&0x1F)),
				B: uint8(random.Uint64() & 0xFF),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: encodeQuality}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
