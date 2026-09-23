// Package token は bypass token（FR-AUTH-7）と checker token（FR-E-4）を実装する。
// 形式: base64url(HMAC-SHA384 署名):base64url(ペイロード)、パディングなし。
// isucon14-portal の app/models/bypass_token.rb / checker_token.rb と互換。
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
)

func sign(secret, data []byte) []byte {
	mac := hmac.New(sha512.New384, secret)
	mac.Write(data)
	return mac.Sum(nil)
}

// pack は Ruby の `"#{sig}:#{payload}".tr('+/','-_').gsub(/=/,”)` と同じ。
// 標準 base64 でエンコードしてから URL-safe に置換し、パディングを落とす。
func pack(signature, content []byte) string {
	s := base64.StdEncoding.EncodeToString(signature) + ":" + base64.StdEncoding.EncodeToString(content)
	s = strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(s)
	return s
}

// unpack は signature と payload のバイナリを取り出す。
func unpack(s string) (signature, payload []byte, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil, nil, false
	}
	dec := func(in string) ([]byte, bool) {
		in = strings.NewReplacer("-", "+", "_", "/").Replace(in)
		if m := len(in) % 4; m != 0 {
			in += strings.Repeat("=", 4-m)
		}
		b, err := base64.StdEncoding.DecodeString(in)
		return b, err == nil
	}
	sig, ok1 := dec(parts[0])
	pl, ok2 := dec(parts[1])
	if !ok1 || !ok2 {
		return nil, nil, false
	}
	return sig, pl, true
}

// CreateBypass は BypassTokenPayload に署名してトークン文字列を作る。
func CreateBypass(secret []byte, payload *pbmisc.BypassTokenPayload) (string, error) {
	filler := make([]byte, 6)
	if _, err := rand.Read(filler); err != nil {
		return "", err
	}
	payload.Filler = base64.StdEncoding.EncodeToString(filler)
	bin, err := proto.Marshal(payload)
	if err != nil {
		return "", err
	}
	return pack(sign(secret, bin), bin), nil
}

// VerifyBypass はトークンを検証してペイロードを返す。無効・期限切れは nil。
func VerifyBypass(secret []byte, s string, now time.Time) *pbmisc.BypassTokenPayload {
	sig, payloadBin, ok := unpack(s)
	if !ok {
		return nil
	}
	if !hmac.Equal(sig, sign(secret, payloadBin)) {
		return nil
	}
	payload := &pbmisc.BypassTokenPayload{}
	if err := proto.Unmarshal(payloadBin, payload); err != nil {
		return nil
	}
	if payload.Expiry < now.Unix() {
		return nil
	}
	return payload
}

// BypassAllows は payload が usage を許可しているか。
func BypassAllows(payload *pbmisc.BypassTokenPayload, usage pbmisc.BypassTokenPayload_Usage) bool {
	if payload == nil {
		return false
	}
	for _, u := range payload.Usages {
		if u == usage {
			return true
		}
	}
	return false
}

// CheckerPayload は checker token の JSON ペイロード。
type CheckerPayload struct {
	TeamID int64 `json:"team_id"`
	Expiry int64 `json:"expiry"`
}

// CreateChecker は JSON ペイロードに署名してトークン文字列を作る。
func CreateChecker(secret []byte, payload CheckerPayload) (string, error) {
	bin, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return pack(sign(secret, bin), bin), nil
}

// VerifyChecker はトークンを検証してペイロードを返す。
// 期限切れチェックは Rails 同様に無効化されている（感想戦対応の FIXME を踏襲）。
func VerifyChecker(secret []byte, s string) *CheckerPayload {
	sig, payloadBin, ok := unpack(s)
	if !ok {
		return nil
	}
	if !hmac.Equal(sig, sign(secret, payloadBin)) {
		return nil
	}
	payload := &CheckerPayload{}
	if err := json.Unmarshal(payloadBin, payload); err != nil {
		return nil
	}
	return payload
}
