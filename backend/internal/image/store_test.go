package image_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
)

type inProcessSanitizer struct{ failure error }

func (s inProcessSanitizer) Sanitize(_ context.Context, input io.Reader, output io.Writer) error {
	if s.failure != nil {
		return s.failure
	}
	decoded, _, err := image.Decode(input)
	if err != nil {
		return isurealimage.ErrUnsupportedMedia
	}
	return jpeg.Encode(output, decoded, &jpeg.Options{Quality: 90})
}

func TestStoreValidatesPurposeMIMEAndEncodedByteBoundaries(t *testing.T) {
	t.Parallel()
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	validJPEG := makeJPEG(t, 8, 8)
	cases := []struct {
		name    string
		purpose isurealimage.Purpose
		input   []byte
		want    error
	}{
		{"post jpeg", isurealimage.PostImage, validJPEG, nil},
		{"profile jpeg", isurealimage.ProfileImage, validJPEG, nil},
		{"realmoji jpeg", isurealimage.RealMojiImage, validJPEG, nil},
		{"post exact limit", isurealimage.PostImage, padTo(validJPEG, 10<<20), nil},
		{"profile exact limit", isurealimage.ProfileImage, padTo(validJPEG, 2<<20), nil},
		{"non jpeg", isurealimage.PostImage, []byte("not a jpeg"), isurealimage.ErrUnsupportedMedia},
		{"post too large", isurealimage.PostImage, append(validJPEG, bytes.Repeat([]byte{0}, 10<<20)...), isurealimage.ErrPayloadTooLarge},
		{"profile too large", isurealimage.ProfileImage, append(validJPEG, bytes.Repeat([]byte{0}, 2<<20)...), isurealimage.ErrPayloadTooLarge},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := isurealimage.NewStore(t.TempDir(), inProcessSanitizer{}, fixedIDs{})
			_, err := store.Save(context.Background(), owner, test.purpose, bytes.NewReader(test.input))
			if !errors.Is(err, test.want) {
				t.Fatalf("Save() error = %v, want %v", err, test.want)
			}
		})
	}
}

func padTo(input []byte, size int) []byte {
	result := append([]byte(nil), input...)
	return append(result, make([]byte, size-len(result))...)
}

func TestStoreStripsMetadataPublishesAtomicallyAndAcceptsLargeDimensions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	input := appendMetadataSegment(t, makeJPEG(t, 8192, 1), []byte("secret-camera-metadata"))
	store := isurealimage.NewStore(root, inProcessSanitizer{}, fixedIDs{})
	object, err := store.Save(context.Background(), owner, isurealimage.PostImage, bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if object.MediaType != "image/jpeg" || object.ByteSize <= 0 || len(object.SHA256) != 64 {
		t.Fatalf("object = %#v", object)
	}
	stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(object.ObjectKey)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("secret-camera-metadata")) {
		t.Fatal("metadata survived re-encoding")
	}
	if _, err := jpeg.Decode(bytes.NewReader(stored)); err != nil {
		t.Fatalf("stored JPEG: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".tmp"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}

func TestStoreFailureLeavesNoPublicationAndGCRemovesOnlyOrphans(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	store := isurealimage.NewStore(root, inProcessSanitizer{failure: isurealimage.ErrSanitizerUnavailable}, fixedIDs{})
	if _, err := store.Save(context.Background(), owner, isurealimage.PostImage, bytes.NewReader(makeJPEG(t, 8, 8))); !errors.Is(err, isurealimage.ErrSanitizerUnavailable) {
		t.Fatalf("failure = %v", err)
	}
	if files := regularFiles(t, root); len(files) != 0 {
		t.Fatalf("partial files: %v", files)
	}

	store = isurealimage.NewStore(root, inProcessSanitizer{}, sequenceIDs{})
	first, _ := store.Save(context.Background(), owner, isurealimage.PostImage, bytes.NewReader(makeJPEG(t, 8, 8)))
	second, _ := store.Save(context.Background(), owner, isurealimage.PostImage, bytes.NewReader(makeJPEG(t, 9, 9)))
	if err := store.GC(context.Background(), map[string]struct{}{first.ObjectKey: {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(first.ObjectKey))); err != nil {
		t.Fatalf("referenced file removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(second.ObjectKey))); !os.IsNotExist(err) {
		t.Fatalf("orphan still exists: %v", err)
	}
}

type fixedIDs struct{}

func (fixedIDs) New() uuid.UUID { return uuid.MustParse("00000000-0000-0000-0000-000000000099") }

type sequenceIDs struct{ next byte }

func (s sequenceIDs) New() uuid.UUID { s.next++; return uuid.New() }

func makeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{R: byte(x), G: byte(y), B: 127, A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, picture, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func appendMetadataSegment(t *testing.T, input, metadata []byte) []byte {
	t.Helper()
	if len(input) < 2 || input[0] != 0xff || input[1] != 0xd8 {
		t.Fatal("invalid fixture")
	}
	length := len(metadata) + 2
	segment := []byte{0xff, 0xfe, byte(length >> 8), byte(length)}
	segment = append(segment, metadata...)
	return append(append(append([]byte{}, input[:2]...), segment...), input[2:]...)
}

func regularFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() && !strings.Contains(path, string(filepath.Separator)+".tmp"+string(filepath.Separator)) {
			files = append(files, path)
		}
		return nil
	})
	return files
}
