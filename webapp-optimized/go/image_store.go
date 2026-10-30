package isureal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// imageStore keeps the bytes of the images uploaded during a load on the local
// disk of the app that received them (ISUREAL_IMAGE_DIR/run/<id>), outside the
// database. The database row holds the metadata and an empty body. Another
// region that is asked for the image fetches it from the peers once and keeps
// a copy. Initialization discards the directory, so the images of a load
// disappear with it. The initial data's images stay in the database.
type imageStore struct {
	dir string // empty: memory only (tests, no ISUREAL_IMAGE_DIR)
	mu  sync.RWMutex
	mem map[string][]byte
}

var images = newImageCache(imageCacheBytes, imageCacheEntries)
var store = newImageStore(os.Getenv("ISUREAL_IMAGE_DIR"))

func newImageStore(dir string) *imageStore {
	s := &imageStore{mem: map[string][]byte{}}
	if dir != "" {
		s.dir = filepath.Join(dir, "run")
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			slog.Error("image store", "dir", s.dir, "error", err)
			s.dir = ""
		}
	}
	return s
}

func (s *imageStore) path(id string) string {
	return filepath.Join(s.dir, id)
}

// put writes an uploaded image. The file appears atomically.
func (s *imageStore) put(id string, data []byte) error {
	if s.dir == "" {
		s.mu.Lock()
		s.mem[id] = bytes.Clone(data)
		s.mu.Unlock()
		return nil
	}
	tmp := s.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(id))
}

// get returns a locally stored image, or ok=false.
func (s *imageStore) get(id string) ([]byte, bool) {
	if !validImageID(id) {
		return nil, false
	}
	if s.dir == "" {
		s.mu.RLock()
		data, ok := s.mem[id]
		s.mu.RUnlock()
		return data, ok
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Error("image store read", "id", id, "error", err)
		}
		return nil, false
	}
	return data, true
}

// clear discards the images of the load. The directory is renamed at once
// and removed in the background, so initialization does not wait for it.
func (s *imageStore) clear() {
	s.mu.Lock()
	s.mem = map[string][]byte{}
	s.mu.Unlock()
	if s.dir == "" {
		return
	}
	old := s.dir + ".old." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(s.dir, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("image store clear", "error", err)
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Error("image store", "dir", s.dir, "error", err)
	}
	go func() { _ = os.RemoveAll(old) }()
}

// validImageID keeps IDs to the UUID alphabet, so an ID is never a path.
func validImageID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}

// fetchFromPeers asks the other regions' apps for an image they received.
// The first copy wins; it is kept locally for later requests.
func fetchFromPeers(ctx context.Context, id string) ([]byte, bool) {
	if len(syncPeers) == 0 || !validImageID(id) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type answer struct {
		data []byte
		ok   bool
	}
	answers := make(chan answer, len(syncPeers))
	for _, peer := range syncPeers {
		go func() {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer+"/internal/images/"+id, nil)
			if err != nil {
				answers <- answer{}
				return
			}
			request.Header.Set("X-Isureal-Sync", syncToken)
			response, err := syncClient.Do(request)
			if err != nil {
				answers <- answer{}
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				answers <- answer{}
				return
			}
			data, err := io.ReadAll(response.Body)
			answers <- answer{data: data, ok: err == nil}
		}()
	}
	for range syncPeers {
		if a := <-answers; a.ok {
			if err := store.put(id, a.data); err != nil {
				slog.Error("image store", "id", id, "error", err)
			}
			return a.data, true
		}
	}
	return nil, false
}

// imageBytes resolves the body of an image whose database row has none:
// this app's store, then the peers.
func imageBytes(ctx context.Context, id string) ([]byte, bool) {
	if data, ok := store.get(id); ok {
		return data, true
	}
	return fetchFromPeers(ctx, id)
}

// storeUploadedImage keeps an uploaded image locally before its metadata is
// written, under the ID the database row will use.
func storeUploadedImage(id string, data []byte) error {
	return store.put(id, data)
}

// cacheUploadedImage adds a just-uploaded, live image of a known owner to the
// memory cache, so its first requests do not read the disk.
func cacheUploadedImage(id, secretKeyHash string, data []byte) {
	images.put(id, imageObject{SecretKeyHash: secretKeyHash, MediaType: "image/jpeg", Live: true, Data: data}, time.Now())
}
