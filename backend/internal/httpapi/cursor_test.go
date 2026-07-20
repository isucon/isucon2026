package httpapi_test

import (
	"errors"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/httpapi"
)

func TestCursorRoundTripAndBinding(t *testing.T) {
	t.Parallel()
	codec, err := httpapi.NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode(httpapi.Cursor{List: "friends", Direction: httpapi.Descending, Values: []string{"2026-07-19T12:00:00Z", "00000000-0000-0000-0000-000000000001"}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(token, "friends", httpapi.Descending)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.List != "friends" || len(decoded.Values) != 2 || decoded.Values[1] != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if _, err := codec.Decode(token, "followers", httpapi.Descending); !errors.Is(err, httpapi.ErrCursorBinding) {
		t.Fatalf("list binding error = %v", err)
	}
	if _, err := codec.Decode(token, "friends", httpapi.Ascending); !errors.Is(err, httpapi.ErrCursorBinding) {
		t.Fatalf("direction binding error = %v", err)
	}
}

func TestCursorRejectsTamperingAndMalformedValues(t *testing.T) {
	t.Parallel()
	codec, _ := httpapi.NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	token, _ := codec.Encode(httpapi.Cursor{List: "posts", Direction: httpapi.Descending, Values: []string{"value"}})
	tampered := token[:len(token)-1] + "A"
	if tampered == token {
		tampered = token[:len(token)-1] + "B"
	}
	if _, err := codec.Decode(tampered, "posts", httpapi.Descending); !errors.Is(err, httpapi.ErrInvalidCursor) {
		t.Fatalf("tamper error = %v", err)
	}
	for _, malformed := range []string{"", "not-base64", token + ".extra"} {
		if _, err := codec.Decode(malformed, "posts", httpapi.Descending); !errors.Is(err, httpapi.ErrInvalidCursor) {
			t.Fatalf("Decode(%q) error = %v", malformed, err)
		}
	}
}

func TestPageLimitBounds(t *testing.T) {
	t.Parallel()
	for _, valid := range []int{1, 25, 50} {
		if got, err := httpapi.PageLimit(valid); err != nil || got != valid {
			t.Fatalf("PageLimit(%d) = %d, %v", valid, got, err)
		}
	}
	if got, err := httpapi.PageLimit(0); err != nil || got != 50 {
		t.Fatalf("default PageLimit = %d, %v", got, err)
	}
	for _, invalid := range []int{-1, 51, 100} {
		if _, err := httpapi.PageLimit(invalid); !errors.Is(err, httpapi.ErrInvalidPageLimit) {
			t.Fatalf("PageLimit(%d) error = %v", invalid, err)
		}
	}
}
