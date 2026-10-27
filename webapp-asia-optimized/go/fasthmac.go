package isureal

import (
	"encoding/binary"
	_ "unsafe"
)

// HMAC-SHA256 を 1000 回繰り返す画像署名の高速版。
// メッセージは常に 32 バイト以下（2 回目以降は 32 バイト）なので、ipad/opad を処理した中間状態から
// 1 ブロックずつ圧縮関数を呼べば済む。圧縮関数は標準ライブラリの SHA-NI 実装を直接使う。
type sha256digest struct {
	h   [8]uint32
	x   [64]byte
	nx  int
	len uint64
}

//go:noescape
//go:linkname sha256blockSHANI crypto/internal/fips140/sha256.blockSHANI
func sha256blockSHANI(dig *sha256digest, p []byte)

var sha256init = [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}

func fastImageSignature(key []byte, value []byte) []byte {
	var pad [64]byte
	var ipad, opad sha256digest
	ipad.h, opad.h = sha256init, sha256init
	copy(pad[:], key)
	for i := range pad {
		pad[i] ^= 0x36
	}
	sha256blockSHANI(&ipad, pad[:])
	for i := range pad {
		pad[i] ^= 0x36 ^ 0x5c
	}
	sha256blockSHANI(&opad, pad[:])

	// 最初のメッセージ（"exp:imageID"）は長さが不定なので通常の方法で 1 回目を計算する
	var block [64]byte
	inner := ipad
	msg := value
	for len(msg) >= 64 {
		sha256blockSHANI(&inner, msg[:64])
		msg = msg[64:]
	}
	total := uint64(64 + len(value))
	n := copy(block[:], msg)
	block[n] = 0x80
	if n+1 > 56 {
		sha256blockSHANI(&inner, block[:])
		block = [64]byte{}
	} else {
		clear(block[n+1:])
	}
	binary.BigEndian.PutUint64(block[56:], total*8)
	sha256blockSHANI(&inner, block[:])

	// 以降はメッセージがちょうど 32 バイト
	var b32 [64]byte
	b32[32] = 0x80
	binary.BigEndian.PutUint64(b32[56:], (64+32)*8)
	for i := 0; i < imageSignatureIterations; i++ {
		if i > 0 {
			inner = ipad
			sha256blockSHANI(&inner, b32[:])
		}
		outer := opad
		for j := 0; j < 8; j++ {
			binary.BigEndian.PutUint32(b32[j*4:], inner.h[j])
		}
		sha256blockSHANI(&outer, b32[:])
		for j := 0; j < 8; j++ {
			binary.BigEndian.PutUint32(b32[j*4:], outer.h[j])
		}
	}
	out := make([]byte, 32)
	copy(out, b32[:32])
	return out
}
