package scenario

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

// timetable は負荷走行の時間割。各リージョンは 1 日を period として投稿期間を開き、
// リージョンどうしは offset ずつずらす（asia、europe、america の順）。staggered 日のあと、
// 最後の 1 日（グランプリ）は grandPrix に全リージョンが同時に開く。
//
// 60 秒の走行では period 9 秒、offset 3 秒で、asia は 0/9/18/27/36 秒、europe は
// 3/12/21/30/39 秒、america は 6/15/24/33/42 秒に開き、51 秒にグランプリが全リージョンで
// 同時に始まる。走行の長さを変えると、すべての時刻が比例して変わる。
type timetable struct {
	period    time.Duration
	offset    time.Duration
	staggered int
	grandPrix time.Duration
	length    time.Duration
}

func newTimetable(length time.Duration) timetable {
	return timetable{
		period:    length * 9 / 60,
		offset:    length * 3 / 60,
		staggered: 5,
		grandPrix: length * 51 / 60,
		length:    length,
	}
}

// days は 1 リージョンが開く日の数（グランプリを含む）。
func (t timetable) days() int { return t.staggered + 1 }

// isGrandPrix は day（1 始まり）がグランプリの日か。
func (t timetable) isGrandPrix(day int) bool { return day == t.days() }

// start はリージョン region の day 日目（1 始まり）が始まる、走行の開始からの時刻。
func (t timetable) start(region string, day int) time.Duration {
	if t.isGrandPrix(day) {
		return t.grandPrix
	}
	return t.offset*time.Duration(slices.Index(geo.Regions, region)) + t.period*time.Duration(day-1)
}

// end はリージョン region の day 日目が終わる時刻（次の日が始まるか、走行が終わる）。
func (t timetable) end(region string, day int) time.Duration {
	if day >= t.days() {
		return t.length
	}
	return t.start(region, day+1)
}

// dayAt は走行の開始から elapsed 経った時点の、リージョン region の日（始まる前は 0）。
func (t timetable) dayAt(region string, elapsed time.Duration) int {
	for day := t.days(); day >= 1; day-- {
		if elapsed >= t.start(region, day) {
			return day
		}
	}
	return 0
}

// postDelay は通知を受けてから投稿するまでの待ち。指数分布で、中央値は 1 日の 1/9
// （60 秒の走行で 1 秒）、上限は 1 日の 8/9。u は [0, 1) の一様な値。
func (t timetable) postDelay(u float64) time.Duration {
	median := float64(t.period) / 9
	delay := time.Duration(-math.Log1p(-u) / math.Ln2 * median)
	return min(delay, t.period*8/9)
}

// hashOf は走行の種と parts から決まる値。同じ走行の同じ組には同じ値を返すので、
// ゴルーチンの順序に左右されずに選択を再現できる。
func hashOf(seed uint64, parts ...string) uint64 {
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	_, _ = h.Write(b[:])
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// unit は hashOf を [0, 1) の一様な値にする。
func unit(seed uint64, parts ...string) float64 {
	return float64(hashOf(seed, parts...)>>11) / (1 << 53)
}

// ipPool は走行ごとに無作為な IPv4 アドレスを重ならないように配る。正常なユーザーと
// 不正な利用者で範囲を分けない。
type ipPool struct {
	mu   sync.Mutex
	rng  *rand.Rand
	used map[uint32]bool
}

func newIPPool(seed uint64) *ipPool {
	return &ipPool{rng: rand.New(rand.NewPCG(seed, seed^0x6970)), used: map[uint32]bool{}}
}

// Next は使っていない、公開の IPv4 アドレスを返す。
func (p *ipPool) Next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		ip := p.rng.Uint32()
		if p.used[ip] || reservedIP(ip) {
			continue
		}
		p.used[ip] = true
		return formatIP(ip)
	}
}

// reservedIP は私用、ループバック、文書用、マルチキャストなど、公開でないアドレスか。
func reservedIP(ip uint32) bool {
	for _, r := range [][2]uint32{
		{0x00000000, 8}, {0x0A000000, 8}, {0x64400000, 10}, {0x7F000000, 8}, {0xA9FE0000, 16},
		{0xAC100000, 12}, {0xC0000000, 24}, {0xC0000200, 24}, {0xC0586300, 24}, {0xC0A80000, 16},
		{0xC6120000, 15}, {0xC6336400, 24}, {0xCB007100, 24}, {0xE0000000, 4}, {0xF0000000, 4},
	} {
		if ip>>(32-r[1]) == r[0]>>(32-r[1]) {
			return true
		}
	}
	return ip&0xFF == 0 || ip&0xFF == 0xFF
}

func formatIP(ip uint32) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], ip)
	return netip.AddrFrom4(b).String()
}
