package image

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

type Purpose string

const (
	PostImage     Purpose = "post"
	ProfileImage  Purpose = "profile"
	RealMojiImage Purpose = "realmoji"
)

func (p Purpose) MaxBytes() int64 {
	if p == PostImage {
		return 10 << 20
	}
	if p == ProfileImage || p == RealMojiImage {
		return 2 << 20
	}
	return 0
}

type Object struct {
	ID          uuid.UUID
	OwnerUserID uuid.UUID
	SHA256      string
	ObjectKey   string
	ByteSize    int64
	MediaType   string
}

type Store struct {
	root      string
	sanitizer Sanitizer
	ids       domain.IDGenerator
}

func NewStore(root string, sanitizer Sanitizer, ids domain.IDGenerator) *Store {
	return &Store{root: filepath.Clean(root), sanitizer: sanitizer, ids: ids}
}

func (s *Store) Save(ctx context.Context, owner uuid.UUID, purpose Purpose, input io.Reader) (Object, error) {
	maximum := purpose.MaxBytes()
	if maximum == 0 {
		return Object{}, errors.New("unknown image purpose")
	}
	encoded, err := io.ReadAll(io.LimitReader(input, maximum+1))
	if err != nil {
		return Object{}, err
	}
	if int64(len(encoded)) > maximum {
		return Object{}, ErrPayloadTooLarge
	}
	if len(encoded) < 2 || encoded[0] != 0xff || encoded[1] != 0xd8 {
		return Object{}, ErrUnsupportedMedia
	}
	temporaryDirectory := filepath.Join(s.root, ".tmp")
	if err := os.MkdirAll(temporaryDirectory, 0o700); err != nil {
		return Object{}, err
	}
	temporary, err := os.CreateTemp(temporaryDirectory, "sanitize-*.jpg")
	if err != nil {
		return Object{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := s.sanitizer.Sanitize(ctx, bytes.NewReader(encoded), temporary); err != nil {
		temporary.Close()
		return Object{}, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return Object{}, err
	}
	if err := temporary.Close(); err != nil {
		return Object{}, err
	}
	output, err := os.Open(temporaryPath)
	if err != nil {
		return Object{}, err
	}
	if _, err := jpeg.Decode(bufio.NewReader(output)); err != nil {
		output.Close()
		return Object{}, ErrUnsupportedMedia
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		output.Close()
		return Object{}, err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, output)
	output.Close()
	if err != nil {
		return Object{}, err
	}
	id := s.ids.New()
	objectKey := filepath.ToSlash(filepath.Join(owner.String(), id.String()+".jpg"))
	destination := filepath.Join(s.root, filepath.FromSlash(objectKey))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Object{}, err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return Object{}, err
	}
	return Object{ID: id, OwnerUserID: owner, SHA256: hex.EncodeToString(digest.Sum(nil)), ObjectKey: objectKey, ByteSize: size, MediaType: "image/jpeg"}, nil
}

func (s *Store) Open(objectKey string) (*os.File, error) {
	path, err := s.resolve(objectKey)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *Store) Remove(objectKey string) error {
	path, err := s.resolve(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) GC(ctx context.Context, referenced map[string]struct{}) error {
	return filepath.WalkDir(s.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path == filepath.Join(s.root, ".tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if _, ok := referenced[key]; !ok {
			return os.Remove(path)
		}
		return nil
	})
}

func (s *Store) resolve(objectKey string) (string, error) {
	if objectKey == "" || strings.Contains(objectKey, "\\") {
		return "", errors.New("invalid object key")
	}
	clean := filepath.Clean(filepath.FromSlash(objectKey))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid object key")
	}
	path := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	return path, nil
}
