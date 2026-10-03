package isureal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Deliberately keep the original algorithm independent of the optimized path.
func referenceImageSignature(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	for range 1000 {
		mac := hmac.New(sha256.New, key)
		mac.Write(value)
		value = mac.Sum(nil)
	}
	return value
}

func TestImageSignatureMatchesOriginal(t *testing.T) {
	for _, length := range []int{0, 1, 32, 64, 65, 128} {
		key := bytes.Repeat([]byte{byte(length)}, length)
		for _, id := range []string{"", "image-id", "画像:/?", strings.Repeat("x", 129)} {
			for _, exp := range []int64{math.MinInt64, 0, 1790553600, math.MaxInt64} {
				want := referenceImageSignature(key, id, exp)
				for range 2 { // Miss and hit must both reproduce the original bytes.
					if got := imageSignature(key, id, exp); !bytes.Equal(got, want) {
						t.Fatalf("keyLength=%d image=%q exp=%d got=%x want=%x", length, id, exp, got, want)
					}
				}
			}
		}
	}
}

func TestImageSignatureCacheIsolationAndConcurrency(t *testing.T) {
	keys := [][32]byte{sha256.Sum256([]byte("before reset")), sha256.Sum256([]byte("after reset"))}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			key := keys[i%2]
			id := fmt.Sprintf("image-%d", i%4)
			exp := int64(1790553600 + i%3)
			want := referenceImageSignature(key[:], id, exp)
			for range 5 {
				got := imageSignature(key[:], id, exp)
				if !bytes.Equal(got, want) {
					t.Errorf("signature mismatch for %d", i)
					return
				}
				got[0] ^= 0xff // Mutating the returned buffer must never corrupt a cache entry.
			}
		})
	}
	wg.Wait()
}

func TestSignatureCacheBoundAndEviction(t *testing.T) {
	cache := newSignatureCache(2)
	for i := range 20 {
		key := signatureCacheKey{Exp: int64(i), ImageID: "image"}
		value := sha256.Sum256([]byte(strconv.Itoa(i)))
		cache.put(key, value)
		if got, ok := cache.get(key); !ok || got != value {
			t.Fatal("new entry lost")
		}
		if len(cache.entries) > 2 || cache.recent.Len() > 2 {
			t.Fatal("cache exceeded bound")
		}
	}
	if _, ok := cache.get(signatureCacheKey{Exp: 0, ImageID: "image"}); ok {
		t.Fatal("old entry not evicted")
	}
	recent := signatureCacheKey{Exp: 18, ImageID: "image"}
	cache.get(recent)
	cache.put(signatureCacheKey{Exp: 20, ImageID: "image"}, [32]byte{})
	if _, ok := cache.get(recent); !ok {
		t.Fatal("recently used entry evicted")
	}
	if _, ok := cache.get(signatureCacheKey{Exp: 19, ImageID: "image"}); ok {
		t.Fatal("least recently used entry retained")
	}
}
