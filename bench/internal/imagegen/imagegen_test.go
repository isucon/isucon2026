package imagegen_test

import (
	"bytes"
	"image/jpeg"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/imagegen"
)

func TestJPEGDecodable(t *testing.T) {
	data, err := imagegen.JPEG(64, 64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Fatalf("output does not start with JPEG SOI marker: % X", data[:2])
	}
	picture, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	bounds := picture.Bounds()
	if bounds.Dx() != 64 || bounds.Dy() != 64 {
		t.Fatalf("size = %dx%d, want 64x64", bounds.Dx(), bounds.Dy())
	}
}

func TestJPEGDeterministicPerSeed(t *testing.T) {
	first, err := imagegen.JPEG(32, 32, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := imagegen.JPEG(32, 32, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same seed produced different bytes")
	}
	other, err := imagegen.JPEG(32, 32, 8)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, other) {
		t.Fatal("different seeds produced identical bytes")
	}
}

func TestJPEGUnderRealMojiLimit(t *testing.T) {
	data, err := imagegen.JPEG(32, 32, 1)
	if err != nil {
		t.Fatal(err)
	}
	const realMojiLimit = 2 << 20
	if len(data) >= realMojiLimit {
		t.Fatalf("image size %d exceeds realmoji limit %d", len(data), realMojiLimit)
	}
}
