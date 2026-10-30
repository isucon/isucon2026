package isureal

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"testing"
)

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

// countingReader records how many bytes were read from it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestReadJPEG(t *testing.T) {
	valid := encodeJPEG(t, 512, 3)
	data, ok, err := readJPEG(bytes.NewReader(valid), 512, 0)
	if err != nil || !ok || !bytes.Equal(data, valid) {
		t.Fatalf("valid JPEG: ok=%v err=%v same=%v", ok, err, bytes.Equal(data, valid))
	}
	tall := encodeJPEG(t, 3, 513)
	for name, input := range map[string][]byte{
		"empty": nil, "not JPEG": []byte("not a jpeg"), "truncated": valid[:2],
		"too wide": encodeJPEG(t, 513, 3), "too tall": tall,
	} {
		if data, ok, err := readJPEG(bytes.NewReader(input), 512, 0); err != nil || ok || data != nil {
			t.Fatalf("%s: ok=%v err=%v", name, ok, err)
		}
	}

	// An oversized image is rejected from its header without reading the rest.
	padded := append(append([]byte{}, tall...), make([]byte, 1<<20)...)
	reader := &countingReader{r: bytes.NewReader(padded)}
	if _, ok, err := readJPEG(reader, 512, 0); err != nil || ok {
		t.Fatalf("padded: ok=%v err=%v", ok, err)
	}
	if reader.n >= 64<<10 {
		t.Fatalf("read %d bytes before rejecting", reader.n)
	}
}

func TestReadJPEGClassified(t *testing.T) {
	for _, test := range []struct {
		name  string
		input []byte
		want  jpegRejection
	}{
		{"invalid JPEG", []byte("not a JPEG"), jpegInvalid},
		{"oversized JPEG", encodeJPEG(t, 513, 3), jpegTooLarge},
		{"valid JPEG", encodeJPEG(t, 512, 3), jpegAccepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, got, err := readJPEGClassified(bytes.NewReader(test.input), 512, int64(len(test.input)))
			if err != nil || got != test.want {
				t.Fatalf("rejection = %v, error = %v; want %v", got, err, test.want)
			}
		})
	}
}
