package scenario

import (
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// RatchetConfig はラチェット（P-102）の設定。値はすべて較正までの暫定値。
type RatchetConfig struct {
	// Window は昇格を判定する観測窓の長さ。
	Window time.Duration
	// MinOps は判定に必要な、観測窓のリージョンの操作数。これより少ない窓は判定しない。
	MinOps int64
	// MaxFailRate、MaxReadP99、MaxOpP99、Max503Rate を超えた観測窓では昇格せず、凍結する。
	// MaxReadP99 は読み取りの p99 で、リージョン内で応答できているかを見る。MaxOpP99 は書き込みと
	// タイムアウトを含むすべての操作の p99 で、ロックや接続の待ちで詰まり始めたことを見る。
	MaxFailRate float64
	MaxReadP99  time.Duration
	MaxOpP99    time.Duration
	Max503Rate  float64
	// Step は口コミ: 健全な観測窓のたびに、現在の人数の Step 倍（1 人以上）が流入する。降格は
	// しない。
	Step float64
	// Buzz（地域で話題になる）: リージョンの 1 日の始まり（投稿期間を開いたとき）に、直前に
	// 判定した観測窓に余裕があれば、そのリージョンで話題になる。現在の人数と同じ数（BuzzMin 人
	// 以上）が流入し、ほかのリージョンにもそれぞれの人数の BuzzSpill 倍（切り上げ）が流入する。
	// 余裕は、健全で操作が BuzzMinOps 件以上あり、読み取りとすべての操作の p99 がそれぞれ上限の
	// BuzzHeadroom 倍以下であること。余裕がある限り毎日でも話題になり、遅いアプリは話題に
	// ならない。
	BuzzHeadroom float64
	BuzzMinOps   int64
	BuzzMin      int
	BuzzSpill    float64
	// BuzzFrom は、リージョンが話題になりうる最も早い時刻（負荷走行の開始から、geo.Regions の
	// 順）。話題はアジアから広がり、europe、america の順に届く。
	BuzzFrom [3]time.Duration
}

var DefaultRatchet = RatchetConfig{
	Window:       metrics.DefaultWidth,
	MinOps:       10,
	MaxFailRate:  0.01,
	MaxReadP99:   time.Second,
	MaxOpP99:     3 * time.Second,
	Max503Rate:   0.05,
	Step:         0.1,
	BuzzHeadroom: 0.5,
	BuzzMinOps:   100,
	BuzzMin:      200,
	BuzzSpill:    0.05,
	BuzzFrom:     [3]time.Duration{0, 10 * time.Second, 20 * time.Second},
}

// growth は、現在 users 人のリージョンの健全な観測窓で、口コミで流入する人数。
func (c RatchetConfig) growth(users int) int {
	return max(1, int(math.Ceil(float64(users)*c.Step)))
}

// roomy は観測窓に Buzz できる余裕があるか。
func (c RatchetConfig) roomy(s metrics.Stats) bool {
	ok, judged := c.healthy(s)
	return ok && judged && s.Ops >= c.BuzzMinOps &&
		s.ReadP99 <= time.Duration(float64(c.MaxReadP99)*c.BuzzHeadroom) &&
		s.OpP99 <= time.Duration(float64(c.MaxOpP99)*c.BuzzHeadroom)
}

// friendRatio はレベル level で流入するユーザーのうち、友達つながりで来る割合（残りは
// 認定公開アカウントをフォローする完全な新規）。レベルが上がるほど友達つながりを増やす。
func friendRatio(level int) float64 {
	return min(0.5+0.05*float64(level), 0.8)
}

// buzzFriendRatio は Buzz で流入したユーザーのうち、友達つながりで来る割合の下限。
const buzzFriendRatio = 0.8

// friendRequestsPerNewcomer は友達つながりのユーザーが送る友達申請の数。
const friendRequestsPerNewcomer = 3

// ratchet はリージョンごとのレベルと人数。
type ratchet struct {
	mu     sync.Mutex
	level  map[string]int
	frozen map[string]bool
	// users はリージョンの負荷ユーザーの数（登録中の流入を含む）。
	users map[string]int
	// roomy は、直前に判定した観測窓に余裕があったリージョン。buzzes はリージョンが話題に
	// なった回数。
	roomy  map[string]bool
	buzzes map[string]int
}

func newRatchet() *ratchet {
	return &ratchet{level: map[string]int{}, frozen: map[string]bool{}, users: map[string]int{}, roomy: map[string]bool{}, buzzes: map[string]int{}}
}

// promote は健全な観測窓でリージョンを 1 段昇格させ、口コミで流入する人数を返す。t.mu を
// 持って呼ぶ。
func (t *ratchet) promote(c RatchetConfig, region string) int {
	t.level[region]++
	added := c.growth(t.users[region])
	t.users[region] += added
	return added
}

func (t *ratchet) buzzCounts() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	maps.Copy(out, t.buzzes)
	return out
}

func (t *ratchet) population() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	maps.Copy(out, t.users)
	return out
}

func (t *ratchet) levels() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	maps.Copy(out, t.level)
	return out
}

// healthy は観測窓のリージョンの集計が昇格の条件を満たすか。操作が少なすぎて判定しない
// ときは judged が false。
func (c RatchetConfig) healthy(s metrics.Stats) (ok, judged bool) {
	if s.Ops < c.MinOps {
		return false, false
	}
	return s.FailureRate() <= c.MaxFailRate &&
		(s.Reads == 0 || s.ReadP99 <= c.MaxReadP99) &&
		s.OpP99 <= c.MaxOpP99 &&
		float64(s.Saw503)/float64(s.Ops) <= c.Max503Rate, true
}

// runRatchet は観測窓が閉じるたびにリージョンごとに判定し、健全なら口コミで新しいユーザーを
// 流入させ、余裕があるかを Buzz のために覚えておく。条件を満たさない窓があれば凍結し、
// 連続する 2 つの窓で条件を満たすまで昇格しない。降格はしない。
func (r *Runner) runRatchet(ctx context.Context, start time.Time, join func(region string, level, users int, buzz bool)) {
	for k := 0; ; k++ {
		if err := sleepUntil(ctx, start.Add(time.Duration(k+1)*r.ratchetConfig.Window)); err != nil {
			return
		}
		// 窓を閉じて、まだ処理中の操作も所要時間に含める。
		r.recorder.Seal(k)
		stats := map[string]metrics.Stats{}
		for _, s := range r.recorder.Regions(k) {
			stats[s.Region] = s
		}
		for _, region := range geo.Regions {
			s := stats[region]
			ok, judged := r.ratchetConfig.healthy(s)
			t := r.ratchet
			t.mu.Lock()
			if judged {
				t.roomy[region] = r.ratchetConfig.roomy(s)
			}
			added := 0
			switch {
			case !judged:
			case !ok:
				t.frozen[region] = true
			case t.frozen[region]:
				t.frozen[region] = false
			default:
				added = t.promote(r.ratchetConfig, region)
			}
			level, users := t.level[region], t.users[region]
			t.mu.Unlock()
			if added > 0 {
				r.cfg.Logger.Info("level up", "region", region, "level", level, "added", added, "users", users)
				join(region, level, added, false)
			}
		}
		r.progress()
	}
}

// maybeBuzz は region が投稿期間を開いた（1 日が始まった）ときに呼び、直前の観測窓に余裕が
// あれば、そのリージョンで話題にする。
func (r *Runner) maybeBuzz(region string) {
	c := r.ratchetConfig
	if r.join == nil || time.Since(r.loadStart) < c.BuzzFrom[slices.Index(geo.Regions, region)] {
		return
	}
	t := r.ratchet
	t.mu.Lock()
	if !t.roomy[region] {
		t.mu.Unlock()
		return
	}
	// 同じ観測窓の余裕で 2 回は話題にならない。
	t.roomy[region] = false
	t.buzzes[region]++
	type inflow struct {
		region       string
		level, added int
		buzz         bool
	}
	added := max(t.users[region], c.BuzzMin)
	inflows := []inflow{{region, t.level[region], added, true}}
	t.users[region] += added
	for _, other := range geo.Regions {
		if other != region {
			spill := int(math.Ceil(float64(t.users[other]) * c.BuzzSpill))
			t.users[other] += spill
			inflows = append(inflows, inflow{other, t.level[other], spill, false})
		}
	}
	users := t.users[region]
	t.mu.Unlock()
	r.cfg.Logger.Info(fmt.Sprintf("%s でバズりました", region), "added", added, "users", users)
	for _, in := range inflows {
		if in.added > 0 {
			r.join(in.region, in.level, in.added, in.buzz)
		}
	}
}

// runNewcomer は負荷走行の途中で登録するユーザー。登録したら関係を作り、開いている投稿期間に
// そのまま投稿してから、ほかのユーザーと同じループに入る。
// buzz なら地域で話題になって流入したユーザーで、多くは友達つながりで来る。
func (r *Runner) runNewcomer(ctx context.Context, region string, level int, buzz bool) {
	r.mu.Lock()
	r.newcomers++
	seq := r.newcomers
	r.mu.Unlock()
	user := agent.New(r.api, region, seq)
	user.API = user.API.WithObserver(r.recorder)
	name := fmt.Sprintf("bench_%s_n%04d", region, seq)
	if err := user.Register(ctx, name, name); err != nil {
		r.requestError(ctx, "%s: 新しいユーザーを登録できません: %v", name, err)
		return
	}
	r.model.AddUser(model.User{ID: user.ID, Username: user.Username, SecretKey: user.SecretKey, Region: region})
	ratio := friendRatio(level)
	if buzz {
		ratio = max(ratio, buzzFriendRatio)
	}
	if rand.Float64() < ratio {
		r.requestFriends(ctx, user)
	} else {
		r.followCertified(ctx, user)
	}
	r.addAgent(user)
	r.runUser(ctx, user, true)
}

// requestFriends は既存の負荷ユーザーに友達申請を送る。相手は友達の多い人ほど選ばれやすく
// （優先的選択）、8 割は同じリージョンから選ぶ。申請は相手が閲覧の合間に承認する。
func (r *Runner) requestFriends(ctx context.Context, user *agent.Agent) {
	candidates := r.agentsSnapshot()
	// 関係はまず地域の中でできるので、相手の 8 割は同じリージョンから選ぶ。
	home := user.Region
	var targets []*agent.Agent
	for attempts := 0; len(targets) < friendRequestsPerNewcomer && attempts < 50 && len(candidates) > 0; attempts++ {
		target := r.pickByFriends(candidates)
		if target.ID == user.ID || slices.Contains(targets, target) || (target.Region != home && rand.Float64() < 0.8) {
			continue
		}
		targets = append(targets, target)
	}
	for _, target := range targets {
		if _, err := user.API.CreateFriendRequest(ctx, user.Token, target.ID); err != nil {
			r.requestError(ctx, "%s: 友達申請を送れません: %v", user.Username, err)
			continue
		}
		r.board.Add(score.TagFriendRequest)
	}
}

// pickByFriends は友達の数 + 1 に比例した重みで 1 人選ぶ。
func (r *Runner) pickByFriends(candidates []*agent.Agent) *agent.Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	weights := make([]int, len(candidates))
	total := 0
	for i, c := range candidates {
		weights[i] = r.friendCount[c.ID] + 1
		total += weights[i]
	}
	x := rand.IntN(total)
	for i, w := range weights {
		if x < w {
			return candidates[i]
		}
		x -= w
	}
	return candidates[len(candidates)-1]
}

// followCertified は初期データの認定公開アカウントを 1〜3 件フォローする。
func (r *Runner) followCertified(ctx context.Context, user *agent.Agent) {
	var certified []string
	for _, u := range r.dataset.Users {
		if u.AccountType == "certified_public" && u.Status == "active" {
			certified = append(certified, u.ID)
		}
	}
	rand.Shuffle(len(certified), func(i, j int) { certified[i], certified[j] = certified[j], certified[i] })
	for _, id := range certified[:min(len(certified), 1+rand.IntN(3))] {
		w := r.model.BeginFollow(user.ID, id)
		err := user.API.Follow(ctx, user.Token, id)
		settle(w, err)
		if err != nil {
			r.requestError(ctx, "%s: 認定公開アカウントをフォローできません: %v", user.Username, err)
			continue
		}
		r.board.Add(score.TagFollow)
	}
}

// acceptRequests は届いている友達申請を読み、すべて承認する。
func (r *Runner) acceptRequests(ctx context.Context, user *agent.Agent) {
	page, err := user.API.Relationships(ctx, user.Token, "incoming-requests", nil)
	if err != nil {
		r.requestError(ctx, "%s: 届いた友達申請を取得できません: %v", user.Username, err)
		return
	}
	for _, request := range page.Items {
		if r.model.User(request.User.ID) == nil {
			r.invalid(ctx, user.Region, "%s: bench の知らないユーザー %s から友達申請が届いています", user.Username, request.User.ID)
			continue
		}
		w := r.model.BeginFriendship(request.User.ID, user.ID, true)
		_, err := user.API.TransitionFriendRequest(ctx, user.Token, request.RelationID, "accept")
		settle(w, err)
		if err != nil {
			r.requestError(ctx, "%s: 友達申請を承認できません: %v", user.Username, err)
			continue
		}
		r.addFriendship(request.User.ID, user.ID)
		r.board.Add(score.TagFriendAccept)
	}
}

func (r *Runner) addFriendship(a, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.friendCount[a]++
	r.friendCount[b]++
}

func (r *Runner) addAgent(user *agent.Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[user.Region] = append(r.agents[user.Region], user)
}

func (r *Runner) agentsSnapshot() []*agent.Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*agent.Agent
	for _, region := range geo.Regions {
		out = append(out, r.agents[region]...)
	}
	return out
}
