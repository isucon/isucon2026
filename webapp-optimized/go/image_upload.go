package isureal

import (
	"bytes"
	"image/jpeg"
	"io"
)

// readJPEG reads an uploaded image. Only the JPEG header is checked, so an image
// that is not a JPEG or exceeds maxSide is rejected before the rest is read.
// sizeHint (the request's content length, when known) sizes the buffer once
// instead of growing it by doubling, which zeroed hundreds of KB per upload.
func readJPEG(r io.Reader, maxSide int, sizeHint int64) (data []byte, ok bool, err error) {
	var buffer bytes.Buffer
	if sizeHint > 0 && sizeHint <= 64<<20 {
		buffer.Grow(int(sizeHint) + 512)
	}
	config, decodeErr := jpeg.DecodeConfig(io.TeeReader(r, &buffer))
	if decodeErr != nil || config.Width > maxSide || config.Height > maxSide {
		return nil, false, nil
	}
	if _, err := buffer.ReadFrom(r); err != nil {
		return nil, false, err
	}
	return buffer.Bytes(), true, nil
}
