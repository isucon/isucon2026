package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const imageURLLifetime = 24 * time.Hour

// Expiration times are rounded down to this step, so every URL for an image
// issued within the step shares one signature (and one cache entry). The URL
// still expires within imageURLLifetime.
const imageURLExpiryStep = 6 * time.Hour
const imageSignatureIterations = 1000

func imageSigningKey(secretKeyHash string) ([]byte, error) {
	key, err := hex.DecodeString(secretKeyHash)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("invalid image signing key")
	}
	return key, nil
}

func imageSignature(key []byte, imageID string, exp int64) []byte {
	if value, ok := history.imageSignature(imageID, exp); ok {
		return value[:]
	}
	// Cache only the pure cryptographic result. Authorization, deletion and
	// expiration checks remain in getImageHandler on every request.
	if len(key) == sha256.Size && len(imageID) <= 128 {
		cacheKey := signatureCacheKey{Key: [sha256.Size]byte(key), ImageID: imageID, Exp: exp}
		if value, ok := imageSignatures.get(cacheKey); ok {
			return value[:]
		}
		value := computeImageSignature(key, imageID, exp)
		imageSignatures.put(cacheKey, value)
		return value[:]
	}
	value := computeImageSignature(key, imageID, exp)
	return value[:]
}

func computeImageSignature(key []byte, imageID string, exp int64) [sha256.Size]byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	mac := hmac.New(sha256.New, key)
	var result [sha256.Size]byte
	for i := 0; i < imageSignatureIterations; i++ {
		if i != 0 {
			mac.Reset()
		}
		mac.Write(value)
		value = mac.Sum(result[:0])
	}
	return result
}

func imageURLExpiry(now time.Time) int64 {
	return now.Add(imageURLLifetime).Truncate(imageURLExpiryStep).Unix()
}

func signedImageURL(user User, imageID string) string {
	exp := imageURLExpiry(time.Now())
	return imageURLWith(imageID, exp, [sha256.Size]byte(imageSignature(user.ImageKey, imageID, exp)))
}

func imageURLWith(imageID string, exp int64, sig [sha256.Size]byte) string {
	query := url.Values{
		"exp": {strconv.FormatInt(exp, 10)},
		"sig": {base64.RawURLEncoding.EncodeToString(sig[:])},
	}
	return "/api/images/" + url.PathEscape(imageID) + "?" + query.Encode()
}
