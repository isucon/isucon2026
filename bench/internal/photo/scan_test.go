package photo

import (
	"bytes"
	"image"
	"image/color"
	jpegStd "image/jpeg"
	"math/rand/v2"
	"testing"
)

// noisy は AC 係数が多く残るノイズ画像を JPEG にする。
func noisy(t *testing.T, gray bool) []byte {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	rect := image.Rect(0, 0, 203, 151)
	var img image.Image
	if gray {
		g := image.NewGray(rect)
		for i := range g.Pix {
			g.Pix[i] = uint8(rng.IntN(256))
		}
		img = g
	} else {
		c := image.NewRGBA(rect)
		for y := range rect.Dy() {
			for x := range rect.Dx() {
				c.SetRGBA(x, y, color.RGBA{uint8(rng.IntN(256)), uint8(x), uint8(y), 255})
			}
		}
		img = c
	}
	var buffer bytes.Buffer
	if err := jpegStd.Encode(&buffer, img, &jpegStd.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decodeRGBA(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	img, err := jpegStd.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("marked JPEG does not decode: %v", err)
	}
	out := image.NewRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func TestFlipsChangePixelsSlightly(t *testing.T) {
	for _, gray := range []bool{false, true} {
		data := noisy(t, gray)
		f, err := findFlips(data)
		if err != nil {
			t.Fatal(err)
		}
		windows := 0
		for _, candidates := range f {
			if len(candidates) > 0 {
				windows++
			}
		}
		if windows < len(data)/windowSize/2 {
			t.Fatalf("gray=%v: only %d of %d windows can be flipped", gray, windows, len(f))
		}
		insert, _ := insertPoint(data)
		j := jpeg{data: data, insert: insert, flips: f}
		a, b := j.mark(1), j.mark(2)
		if !bytes.Equal(a, j.mark(1)) {
			t.Fatal("the same key gave different bytes")
		}
		original, pa, pb := decodeRGBA(t, data), decodeRGBA(t, a), decodeRGBA(t, b)
		if bytes.Equal(pa.Pix, pb.Pix) {
			t.Fatalf("gray=%v: two keys gave the same pixels", gray)
		}
		var total, worst int
		for i := range original.Pix {
			d := int(original.Pix[i]) - int(pa.Pix[i])
			d = max(d, -d)
			total += d
			worst = max(worst, d)
		}
		if mean := float64(total) / float64(len(original.Pix)); mean > 1 || worst > 48 {
			t.Fatalf("gray=%v: pixels changed too much: mean %.3f, worst %d", gray, mean, worst)
		}
	}
}

func TestFlipsFixByteStuffing(t *testing.T) {
	data := []byte{0x10, 0xFE, 0x20, 0xFF, 0x00, 0x30}
	f := flips{{{off: 1, mask: 0x01}}, {{off: 3, mask: 0x01}}}
	// 窓ごとに候補が 1 つなので、どの key でも両方を反転する。
	got := f.apply(nil, data, 0, 7)
	want := []byte{0x10, 0xFF, 0x00, 0x20, 0xFE, 0x30}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestFindFlipsSkipsProgressive(t *testing.T) {
	data := noisy(t, false)
	// SOF0 を SOF2 に書き換えると、プログレッシブとして扱う。
	i := bytes.Index(data, []byte{0xFF, 0xC0})
	progressive := bytes.Clone(data)
	progressive[i+1] = 0xC2
	f, err := findFlips(progressive)
	if err != nil || f != nil {
		t.Fatalf("progressive JPEG: flips=%v err=%v, want none", f != nil, err)
	}
}

func TestFindFlipsRejectsBrokenSOS(t *testing.T) {
	data := noisy(t, false)
	i := bytes.Index(data, []byte{0xFF, 0xDA})
	// SOS の長さを 2（中身なし）にして切る。
	broken := append(bytes.Clone(data[:i]), 0xFF, 0xDA, 0x00, 0x02, 0xFF, 0xD9)
	if _, err := findFlips(broken); err == nil {
		t.Fatal("a broken SOS was accepted")
	}
}
