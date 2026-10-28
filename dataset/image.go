package dataset

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
