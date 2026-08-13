// Package imagegen produces deterministic JPEG fixtures for load generation.
// The application accepts JPEG only (leading FF D8 and decodable by Go's
// image/jpeg), so the fixtures are encoded with the same standard library.
package imagegen

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
)

const encodeQuality = 80

// JPEG renders a width x height gradient-with-noise image. The same seed
// always produces identical bytes so runs are reproducible (CAL-01).
func JPEG(width, height int, seed uint64) ([]byte, error) {
	random := rand.New(rand.NewPCG(seed, seed))
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
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
