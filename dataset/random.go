package dataset

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"
)

// newRand は seed と用途の名前から乱数列を作る。用途ごとに列を分けるので、ある用途の
// 生成手順を変えても、ほかの用途の結果は変わらない。
func newRand(seed uint64, purpose string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(purpose))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}

// dayRand は day 日目の user（通し番号）について、用途ごとの乱数列を作る。ほかの日や
// ユーザーの生成を経ずに、同じ列を何度でも作れる。
func dayRand(seed uint64, purpose string, day, user int) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(purpose))
	return rand.New(rand.NewPCG(seed^h.Sum64(), uint64(day)<<32|uint64(uint32(user))))
}

// idGenerator は UUID v4 の形をした決定的な ID を順に作る。関係の生成に使う。
type idGenerator struct{ r *rand.Rand }

func (g idGenerator) next() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], g.r.Uint64())
	binary.BigEndian.PutUint64(b[8:], g.r.Uint64())
	return formatUUID(b)
}

// derivedID は seed、種類、番号の組から決まる UUID v4 の形の ID。投稿のように単独で
// 作り直すものに使う。
func derivedID(seed uint64, kind string, parts ...int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d:%s", seed, kind)
	for _, part := range parts {
		fmt.Fprintf(h, ":%d", part)
	}
	var b [16]byte
	copy(b[:], h.Sum(nil))
	return formatUUID(b)
}

func formatUUID(b [16]byte) string {
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// HashCredential はアプリと同じく hex(SHA-256(plain)) を返す。ユーザーのシークレット
// キーと管理者のパスワードはこの形で保存される。
func HashCredential(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// secretKey は seed と通し番号から決まるユーザーのシークレットキー。
func secretKey(seed uint64, index int) string {
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], seed)
	mac := hmac.New(sha256.New, key[:])
	fmt.Fprintf(mac, "secret-key:%d", index)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// pick は重みに比例して添字を選ぶ。
func pick(r *rand.Rand, weights []float64) int {
	var total float64
	for _, w := range weights {
		total += w
	}
	x := r.Float64() * total
	for i, w := range weights {
		if x < w {
			return i
		}
		x -= w
	}
	return len(weights) - 1
}

// poisson は平均 mean のポアソン分布に従う整数を返す。
func poisson(r *rand.Rand, mean float64) int {
	limit := math.Exp(-mean)
	product := r.Float64()
	count := 0
	for product > limit {
		product *= r.Float64()
		count++
	}
	return count
}

// between は [from, to) の一様な時刻を返す。PostgreSQL の timestamptz と同じく
// マイクロ秒で切り捨てるので、DB から読んだ値とそのまま比べられる。
func between(r *rand.Rand, from, to time.Time) time.Time {
	span := to.Sub(from)
	if span <= 0 {
		return from
	}
	return from.Add(time.Duration(r.Int64N(int64(span)))).Truncate(time.Microsecond)
}
