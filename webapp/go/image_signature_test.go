package isureal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestImageSignatureVector(t *testing.T) {
	// Python の hashlib/hmac で1000回反復して求めた値と照合する。
	key := sha256.Sum256([]byte("test-secret"))
	got := base64.RawURLEncoding.EncodeToString(imageSignature(key[:], "550e8400-e29b-41d4-a716-446655440000", 1790553600))
	if got != "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84" {
		t.Fatalf("signature = %q", got)
	}
	decoded, err := imageSigningKey(hex.EncodeToString(key[:]))
	if err != nil || string(decoded) != string(key[:]) {
		t.Fatalf("signing key: %x, %v", decoded, err)
	}
	for _, invalid := range []string{"", "hash", "abcd"} {
		if _, err := imageSigningKey(invalid); err == nil {
			t.Fatalf("accepted invalid stored key %q", invalid)
		}
	}
}

func TestSignedImageURLLifetime(t *testing.T) {
	key := sha256.Sum256([]byte("test-secret"))
	user := User{ID: "owner", Region: "asia", ImageKey: key[:]}
	before := time.Now().Unix()
	link, err := url.Parse(signedImageURL(user, "image-id"))
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().Unix()
	query := link.Query()
	exp, err := strconv.ParseInt(query.Get("exp"), 10, 64)
	if err != nil || exp < before+86400 || exp > after+86400 {
		t.Fatalf("expiry = %d, want issuance + 24h", exp)
	}
	if link.Path != "/api/images/image-id" || len(query) != 2 {
		t.Fatalf("unexpected URL: %s", link)
	}
	want := base64.RawURLEncoding.EncodeToString(imageSignature(key[:], "image-id", exp))
	if query.Get("sig") != want {
		t.Fatal("URL signature mismatch")
	}
}

func TestImageRejectsMalformedAndExpiredURL(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	// 不正な URL は DB にアクセスする前に拒否する。
	e.GET("/api/images/:imageId", getImageHandler(&HandlerContext{}))
	now := time.Now().Unix()
	key := sha256.Sum256([]byte("test-secret"))
	sign := func(exp int64) string {
		return base64.RawURLEncoding.EncodeToString(imageSignature(key[:], "image", exp))
	}
	future := strconv.FormatInt(now+86400, 10)
	for name, query := range map[string]string{
		"missing":              "",
		"missing signature":    "exp=" + future,
		"missing expiry":       "sig=" + sign(now+86400),
		"invalid expiry":       "exp=tomorrow&sig=x",
		"overflow":             "exp=999999999999999999999999&sig=x",
		"leading zero":         "exp=0" + future + "&sig=x",
		"invalid signature":    "exp=" + future + "&sig=***",
		"short signature":      "exp=" + future + "&sig=YQ",
		"newline in signature": "exp=" + future + "&sig=" + sign(now+86400) + "%0A",
		"duplicate expiry":     "exp=" + future + "&exp=" + future + "&sig=x",
		"duplicate signature":  "exp=" + future + "&sig=x&sig=y",
		"malformed query":      "exp=" + future + "&sig=%zz",
		"expired":              "exp=" + strconv.FormatInt(now-1, 10) + "&sig=" + sign(now-1),
		"expiry boundary":      "exp=" + strconv.FormatInt(now, 10) + "&sig=" + sign(now),
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/images/image?"+query, nil))
			if rec.Code != http.StatusForbidden || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}
