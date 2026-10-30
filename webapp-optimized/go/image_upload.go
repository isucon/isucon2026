package isureal

import (
	"bytes"
	"image/jpeg"
	"io"
)

type jpegRejection uint8

const (
	jpegAccepted jpegRejection = iota
	jpegInvalid
	jpegTooLarge
)

// readJPEG checks the JPEG header and dimensions before reading the rest.
func readJPEG(r io.Reader, maxSide int, sizeHint int64) (data []byte, ok bool, err error) {
	data, rejection, err := readJPEGClassified(r, maxSide, sizeHint)
	return data, rejection == jpegAccepted, err
}

// readJPEGClassified distinguishes invalid JPEG data from a valid oversized
// image. It allocates for the body only after the header has been accepted.
func readJPEGClassified(r io.Reader, maxSide int, sizeHint int64) (data []byte, rejection jpegRejection, err error) {
	var buffer bytes.Buffer
	config, decodeErr := jpeg.DecodeConfig(io.TeeReader(r, &buffer))
	if decodeErr != nil {
		return nil, jpegInvalid, nil
	}
	if config.Width > maxSide || config.Height > maxSide {
		return nil, jpegTooLarge, nil
	}
	if sizeHint > 0 && sizeHint <= 64<<20 {
		buffer.Grow(int(sizeHint) + 512)
	}
	if _, err := buffer.ReadFrom(r); err != nil {
		return nil, jpegAccepted, err
	}
	return buffer.Bytes(), jpegAccepted, nil
}
