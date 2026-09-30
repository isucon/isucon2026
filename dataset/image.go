package dataset

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
)

// ImageBytes は画像の中身（JPEG）を返す。中身は ID で決まり、画像ごとに違う（同じ中身の
// 画像をまとめても小さくならない）。bench は画像の行から期待する中身を計算できる。
func (w *World) ImageBytes(img *Image) []byte {
	size := w.Config.PostImageSize
	switch img.Kind {
	case ImageRealMoji:
		size = w.Config.RealMojiImageSize
	case ImageProfile:
		size = w.Config.ProfileImageSize
	}
	return renderJPEG(w.Config.Seed, img.ID, size)
}

func renderJPEG(seed uint64, id string, size int) []byte {
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
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: 80}); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}
