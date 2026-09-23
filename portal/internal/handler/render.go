// Package handler は HTTP エンドポイントを提供する。
// レスポンスはすべて protobuf。Content-Type は
// `application/protobuf; proto=<full message name>` 形式（CT-1）。
package handler

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pberrors "github.com/isucon/isucon2026/portal/gen/pb/isuxportal"
)

func renderPB(w http.ResponseWriter, status int, m proto.Message) {
	b, err := proto.Marshal(m)
	if err != nil {
		slog.Error("proto marshal failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	renderRawPB(w, status, m.ProtoReflect().Descriptor().FullName(), b)
}

// renderRawPB はエンコード済み wire（事前生成キャッシュ）をそのまま返す。
func renderRawPB(w http.ResponseWriter, status int, name protoreflect.FullName, wire []byte) {
	w.Header().Set("Content-Type", fmt.Sprintf("application/protobuf; proto=%s", name))
	w.WriteHeader(status)
	_, _ = w.Write(wire)
}

func renderError(w http.ResponseWriter, status int, name, message string) {
	renderErrorFull(w, status, name, message, []string{message})
}

func renderErrorFull(w http.ResponseWriter, status int, name, message string, descriptions []string) {
	renderPB(w, status, &pberrors.Error{
		Code:              int32(status),
		Name:              name,
		HumanMessage:      message,
		HumanDescriptions: descriptions,
	})
}

func renderInternalError(w http.ResponseWriter, err error) {
	slog.Error("internal error", "error", err)
	renderError(w, http.StatusInternalServerError, "InternalServerError", "internal server error")
}

func renderPlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// etagValue は合成 ETag（protobuf バイト列など）から HTTP ETag ヘッダ値を作る。
// ダッシュボードの事前生成キャッシュは SHA-384 ダイジェストを使う（CT-5）。
func etagValue(payload []byte) string {
	sum := sha512.Sum384(payload)
	return etagFromDigest(sum[:])
}

func etagFromDigest(digest []byte) string {
	return `W/"` + hex.EncodeToString(digest[:16]) + `"`
}

// staleCheck は Rails の stale?(etag:) 相当。一致したら 304 を書いて false を返す。
func staleCheckETag(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return false
	}
	return true
}

func staleCheck(w http.ResponseWriter, r *http.Request, payload []byte) bool {
	return staleCheckETag(w, r, etagValue(payload))
}
