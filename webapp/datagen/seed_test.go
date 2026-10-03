package main

import (
	"testing"
	"time"
)

func TestCopyValue(t *testing.T) {
	var nilString *string
	latitude := 35.5
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, `\N`},
		{nilString, `\N`},
		{"a\tb\nc\\d", `a\tb\nc\\d`},
		{"佐藤 花子", "佐藤 花子"},
		{&latitude, "35.5"},
		{[]byte{0xff, 0xd8}, `\\xffd8`},
		{time.Date(2026, 9, 29, 3, 4, 5, 600000000, time.UTC), "2026-09-29 03:04:05.6Z"},
		{date(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)), "2026-09-29"},
	} {
		if got := copyValue(tc.value); got != tc.want {
			t.Errorf("copyValue(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
