// Package metrics は完了した操作を、リージョンごと・固定長の観測窓ごとに
// 集計する（P-115 §6.2）。観測窓は走行を打ち切る可用性 SLO の判定に使い、
// 後でリージョンごとのラチェット（P-102）にも使う。
package metrics

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
)

// DefaultWidth は観測窓の長さ。
const DefaultWidth = 5 * time.Second

// UnknownRegion には、振り分け先の国を持たない操作（管理者など）を集める。
const UnknownRegion = "unknown"

type bucket struct {
	ops      int64
	failures int64
	timeouts int64
	saw503   int64
	reads    []time.Duration
	// latencies はすべての操作（失敗とタイムアウトを含む）の所要時間。
	latencies []time.Duration
}

// Recorder は client.Observer を実装する。操作は完了した時刻の観測窓に入れる。
//
// 完了した操作だけを数えると、詰まって終わらないリクエストは、終わるまで観測窓の p99 に
// 表れない。そこで処理中の操作も持っておき、Seal で観測窓を閉じるときに、その時点で
// 処理中の操作を、閉じた時刻までの経過時間を所要時間として p99 に含める。
type Recorder struct {
	start time.Time
	width time.Duration

	mu       sync.Mutex
	buckets  map[int]map[string]*bucket
	inFlight map[uint64]inFlight
	nextID   uint64
	sealed   map[int]bool
	// routes はリージョンとルートごとの所要時間（運営が較正に使う）。
	routes map[[2]string]*routeStats
}

type routeStats struct {
	ops, failures int64
	durations     []time.Duration
}

// RouteStats はリージョンとルートごとの集計。
type RouteStats struct {
	Region   string        `json:"region"`
	Route    string        `json:"route"`
	Ops      int64         `json:"ops"`
	Failures int64         `json:"failures"`
	P50      time.Duration `json:"p50"`
	P90      time.Duration `json:"p90"`
	P99      time.Duration `json:"p99"`
	Max      time.Duration `json:"max"`
}

// inFlight は処理中の操作。
type inFlight struct {
	region string
	start  time.Time
	read   bool
}

func New(start time.Time, width time.Duration) *Recorder {
	if width <= 0 {
		width = DefaultWidth
	}
	return &Recorder{start: start, width: width, buckets: make(map[int]map[string]*bucket), inFlight: make(map[uint64]inFlight), sealed: make(map[int]bool), routes: map[[2]string]*routeStats{}}
}

// Begin は操作の開始を記録し、完了したときに呼ぶ関数を返す（client.InFlightObserver）。
func (r *Recorder) Begin(route, country string, start time.Time) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := r.nextID
	r.inFlight[id] = inFlight{region: regionOf(country), start: start, read: strings.HasPrefix(route, http.MethodGet+" ")}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.inFlight, id)
	}
}

// Seal は観測窓 window を閉じる。窓の終わりより前に始まって今も処理中の操作を、窓の終わり
// までの経過時間を所要時間として、読み取りとすべての操作の所要時間に加える（操作数と失敗には
// 数えない）。窓の終わりの直後に呼ぶ。同じ窓を 2 度閉じても 1 度だけ加える。処理中の操作が
// あとで完了すると、完了した窓にも数える。
func (r *Recorder) Seal(window int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed[window] {
		return
	}
	r.sealed[window] = true
	windowStart := r.start.Add(time.Duration(window) * r.width)
	end := windowStart.Add(r.width)
	for _, op := range r.inFlight {
		if !op.start.Before(end) {
			continue
		}
		b := r.bucket(windowStart, op.region)
		elapsed := end.Sub(op.start)
		b.latencies = append(b.latencies, elapsed)
		if op.read {
			b.reads = append(b.reads, elapsed)
		}
	}
}

// WindowAt は at を含む観測窓の番号を返す。開始前の時刻は観測窓 0 に入れる。
func (r *Recorder) WindowAt(at time.Time) int {
	return max(int(at.Sub(r.start)/r.width), 0)
}

func (r *Recorder) bucket(at time.Time, region string) *bucket {
	window := r.WindowAt(at)
	regions := r.buckets[window]
	if regions == nil {
		regions = make(map[string]*bucket)
		r.buckets[window] = regions
	}
	b := regions[region]
	if b == nil {
		b = &bucket{}
		regions[region] = b
	}
	return b
}

func regionOf(country string) string {
	if region := geo.RegionOf(country); region != "" {
		return region
	}
	return UnknownRegion
}

func (r *Recorder) Observe(record client.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.bucket(record.Start.Add(record.Duration), regionOf(record.Country))
	b.ops++
	if record.Err != nil && !record.Allowed {
		b.failures++
	}
	if record.Timeout {
		b.timeouts++
	}
	if record.Saw503 {
		b.saw503++
	}
	b.latencies = append(b.latencies, record.Duration)
	if record.Err == nil && strings.HasPrefix(record.Route, http.MethodGet+" ") {
		b.reads = append(b.reads, record.Duration)
	}
	key := [2]string{regionOf(record.Country), record.Route}
	rs := r.routes[key]
	if rs == nil {
		rs = &routeStats{}
		r.routes[key] = rs
	}
	rs.ops++
	if record.Err != nil && !record.Allowed {
		rs.failures++
	}
	rs.durations = append(rs.durations, record.Duration)
}

// Routes はリージョンとルートごとの集計を返す（リージョン、ルートの順）。
func (r *Recorder) Routes() []RouteStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []RouteStats
	for key, rs := range r.routes {
		out = append(out, RouteStats{
			Region: key[0], Route: key[1], Ops: rs.ops, Failures: rs.failures,
			P50: percentile(rs.durations, 0.5), P90: percentile(rs.durations, 0.9), P99: percentile(rs.durations, 0.99), Max: percentile(rs.durations, 1),
		})
	}
	slices.SortFunc(out, func(a, b RouteStats) int {
		if a.Region != b.Region {
			return strings.Compare(a.Region, b.Region)
		}
		return strings.Compare(a.Route, b.Route)
	})
	return out
}

// AddFailure は、HTTP としては成功したがレスポンスの検証に失敗した操作を n 件として数える。
// 操作自体は分母に数え済みなので、分子にだけ足す。一部の操作だけを検証するときは、
// 検証する割合の逆数を n にして、エラー率の期待値を全件を検証したときとそろえる。
func (r *Recorder) AddFailure(region string, at time.Time, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bucket(at, region).failures += n
}

// Stats は 1 つの観測窓における 1 リージョン（Region が "" なら全リージョン）の
// 集計。
type Stats struct {
	Window   int           `json:"window"`
	Region   string        `json:"region,omitempty"`
	Ops      int64         `json:"ops"`
	Failures int64         `json:"failures"`
	Timeouts int64         `json:"timeouts"`
	Saw503   int64         `json:"saw503"`
	Reads    int           `json:"reads"`
	ReadP99  time.Duration `json:"readP99"`
	// OpP99 はすべての操作（書き込み、失敗、タイムアウトを含む）の所要時間の p99。
	OpP99 time.Duration `json:"opP99"`
}

// FailureRate は失敗数 / 操作数。HTTP として成功した操作に検証の失敗を足すので、
// 1 を超えることがある。
func (s Stats) FailureRate() float64 {
	if s.Ops == 0 {
		return 0
	}
	return float64(s.Failures) / float64(s.Ops)
}

func summarize(window int, region string, buckets ...*bucket) Stats {
	stats := Stats{Window: window, Region: region}
	var reads, latencies []time.Duration
	for _, b := range buckets {
		stats.Ops += b.ops
		stats.Failures += b.failures
		stats.Timeouts += b.timeouts
		stats.Saw503 += b.saw503
		reads = append(reads, b.reads...)
		latencies = append(latencies, b.latencies...)
	}
	stats.Reads = len(reads)
	stats.ReadP99 = percentile(reads, 0.99)
	stats.OpP99 = percentile(latencies, 0.99)
	return stats
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	index := int(float64(len(sorted))*p+0.999999) - 1
	return sorted[min(max(index, 0), len(sorted)-1)]
}

// Regions は window のリージョンごとの集計を geo.Regions の順で返す。
// UnknownRegion があれば最後に付ける。
func (r *Recorder) Regions(window int) []Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	regions := r.buckets[window]
	var out []Stats
	for _, region := range append(slices.Clone(geo.Regions), UnknownRegion) {
		if b := regions[region]; b != nil {
			out = append(out, summarize(window, region, b))
		}
	}
	return out
}

// Total は window の全リージョン合算の集計を返す。
func (r *Recorder) Total(window int) Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	var buckets []*bucket
	for _, b := range r.buckets[window] {
		buckets = append(buckets, b)
	}
	return summarize(window, "", buckets...)
}

// Windows はデータのある観測窓の番号を昇順で返す。
func (r *Recorder) Windows() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int
	for window := range r.buckets {
		out = append(out, window)
	}
	slices.Sort(out)
	return out
}

// SLO は P-115 §6.2 の可用性の条件。操作数が MinOps 未満の観測窓は判定しない。
type SLO struct {
	FailRate float64
	MinOps   int64
}

// DefaultSLO は較正までの暫定値。
var DefaultSLO = SLO{FailRate: 0.10, MinOps: 20}

// Violation は upTo より前（upTo は含まない）の観測窓のどれかが slo を破って
// いれば理由を、なければ "" を返す。閉じた観測窓だけを判定するには
// upTo = WindowAt(now)、走行終了時に最後の途中の観測窓も含めるには
// WindowAt(now)+1 を渡す。
func (r *Recorder) Violation(slo SLO, upTo int) string {
	for _, window := range r.Windows() {
		if window >= upTo {
			break
		}
		stats := r.Total(window)
		if stats.Ops < slo.MinOps {
			continue
		}
		if rate := stats.FailureRate(); rate > slo.FailRate {
			return fmt.Sprintf("%s〜%s のエラー率 %.1f%%（%d/%d 件）が上限 %.1f%% を超えました",
				time.Duration(window)*r.width, time.Duration(window+1)*r.width,
				rate*100, stats.Failures, stats.Ops, slo.FailRate*100)
		}
	}
	return ""
}
