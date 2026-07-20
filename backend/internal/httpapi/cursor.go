package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidCursor    = errors.New("invalid cursor")
	ErrCursorBinding    = errors.New("cursor belongs to another list or ordering")
	ErrInvalidPageLimit = errors.New("page limit must be between 1 and 50")
)

type CursorDirection string

const (
	Ascending  CursorDirection = "asc"
	Descending CursorDirection = "desc"
)

type Cursor struct {
	Version   int             `json:"v"`
	List      string          `json:"list"`
	Direction CursorDirection `json:"direction"`
	Values    []string        `json:"values"`
}

type CursorCodec struct{ key []byte }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("cursor HMAC key must contain at least 32 bytes")
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

func (c *CursorCodec) Encode(cursor Cursor) (string, error) {
	if cursor.List == "" || (cursor.Direction != Ascending && cursor.Direction != Descending) || len(cursor.Values) == 0 {
		return "", ErrInvalidCursor
	}
	cursor.Version = 1
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	signature := c.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *CursorCodec) Decode(token, expectedList string, expectedDirection CursorDirection) (Cursor, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Cursor{}, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != parts[0] {
		return Cursor{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] || !hmac.Equal(signature, c.sign(payload)) {
		return Cursor{}, ErrInvalidCursor
	}
	var cursor Cursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.List == "" || len(cursor.Values) == 0 {
		return Cursor{}, ErrInvalidCursor
	}
	if cursor.List != expectedList || cursor.Direction != expectedDirection {
		return Cursor{}, ErrCursorBinding
	}
	return cursor, nil
}

func (c *CursorCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func PageLimit(requested int) (int, error) {
	if requested == 0 {
		return 50, nil
	}
	if requested < 1 || requested > 50 {
		return 0, ErrInvalidPageLimit
	}
	return requested, nil
}
