package testkit

import (
	"encoding/base64"
	"strings"
)

// base64Decode はパディング欠けを許容する標準 base64 デコード（Ruby の unpack1('m*') 相当）。
func base64Decode(s string) ([]byte, error) {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(s)
}
