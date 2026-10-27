package isureal

import (
	"bytes"
	"image/jpeg"
	"io"
)

// readJPEG reads an uploaded image. Only the JPEG header is checked, so an image
// that is not a JPEG or exceeds maxSide is rejected before the rest is read.
func readJPEG(r io.Reader, maxSide int) (data []byte, ok bool, err error) {
	var buffer bytes.Buffer
	config, decodeErr := jpeg.DecodeConfig(io.TeeReader(r, &buffer))
	if decodeErr != nil || config.Width > maxSide || config.Height > maxSide {
		return nil, false, nil
	}
	if _, err := buffer.ReadFrom(r); err != nil {
		return nil, false, err
	}
	return buffer.Bytes(), true, nil
}
