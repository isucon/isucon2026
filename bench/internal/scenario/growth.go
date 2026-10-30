package scenario

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/dataset"
)

// GrowthConfig はユーザーの増え方の設定。
//
// リージョンの 1 日が始まるとき、前日の失敗率が MaxFailRate 以下なら新しいユーザーが来る。
// 前日にゲートを通過した人は、通過までの時間（通知がすぐ届いていたら投稿を始めたはずの
// 時刻から、友達のフィードを読み終えるまで）が短いほど多くの友達を誘う。1 日の FastShare
// 以内なら FastInvites 人、QuickShare で QuickInvites 人、SlowShare 以上なら 0 人で、その
// 間は直線で減る。通過できなかった人は Discourage 人ずつ思いとどまらせる。アプリが
// 詰まり始めると通過が遅くなるので、詰まりきる前に増え方が鈍る。全員がすぐに通過すれば、
// 1 日に 1 + FastInvites 倍になる。最終日（グランプリ）には新しい人は来ず、今いる人で競う。
type GrowthConfig struct {
	FastShare    float64
	QuickShare   float64
	SlowShare    float64
	FastInvites  float64
	QuickInvites float64
	Discourage   float64
	MaxFailRate  float64
	MinOps       int64
	// FriendRatio は新しいユーザーのうち、友達つながりで来る（既存のユーザーに友達申請を
	// 送る）割合。残りは認定公開アカウントをフォローする完全な新規。
	FriendRatio float64
}

// DefaultGrowth は 60 秒の走行（1 日 9 秒）で、0.25 秒以内に通過した人が 2 人、1 秒で 1.5 人、
// 2 秒で 0 人を誘う。
var DefaultGrowth = GrowthConfig{
	FastShare: 1.0 / 36, QuickShare: 1.0 / 9, SlowShare: 2.0 / 9,
	FastInvites: 2, QuickInvites: 1.5, Discourage: 1,
	MaxFailRate: 0.01, MinOps: 300, FriendRatio: 0.7,
}

// invites は、通過までに took かかった人が、1 日が period のときに誘う人数。
func (c GrowthConfig) invites(took, period time.Duration) float64 {
	f := took.Seconds() / period.Seconds()
	switch {
	case f <= c.FastShare:
		return c.FastInvites
	case f <= c.QuickShare:
		return c.FastInvites - (c.FastInvites-c.QuickInvites)*(f-c.FastShare)/(c.QuickShare-c.FastShare)
	case f < c.SlowShare:
		return c.QuickInvites * (c.SlowShare - f) / (c.SlowShare - c.QuickShare)
	default:
		return 0
	}
}

// dayStats はリージョンの 1 日の集計。
type dayStats struct {
	// ops と failures は、その日に始まって終わった操作の数と、そのうち失敗した数。
	ops, failures int64
	// passers はゲートを通過した人と、投稿を始めてから通過するまでの時間。
	passers map[string]time.Duration
	// excused はその日に投稿しなかったが、通過できなかった人には数えない人（自分で休んだ
	// 人と、その日をほかのリージョンで過ごして移ってきた人）。departed はその日のうちに
	// ほかのリージョンへ移った人。rested、arrived はそのうちの休んだ人数と移ってきた人数。
	excused         map[string]bool
	departed        map[string]bool
	rested, arrived int
	// population はその日の始まりのユーザー数、newcomers はその日に来る新しいユーザー数。
	// quick と missed は、前日にゲートを快適に（QuickShare 以内に）通過した人数と、通過
	// できなかった人数。invites は前日に通過した人が誘った人数の合計。
	population int
	newcomers  int
	quick      int
	missed     int
	invites    float64
	// healthy は前日の失敗率が上限以下だったか（2 日目以降）。
	healthy  bool
	failRate float64
}

// DayReport は結果に出すリージョンの 1 日の集計。
type DayReport struct {
	Region     string `json:"region"`
	Day        int    `json:"day"`
	Population int    `json:"population"`
	Newcomers  int    `json:"newcomers"`
	// Quick と Missed は、前日にゲートを 1 秒以内に通過した人数と、通過できなかった人数
	// （新しいユーザーを思いとどまらせた人数）。Invites は前日に通過した人が誘った人数の合計。
	Quick   int     `json:"quick"`
	Missed  int     `json:"missed"`
	Invites float64 `json:"invites"`
	// GateP50 と GateP90 は、その日のゲート通過にかかった時間（通知がすぐ届いていたら投稿を
	// 始めたはずの時刻から）[ns]。
	GateP50 time.Duration `json:"gateP50"`
	GateP90 time.Duration `json:"gateP90"`
	Passes  int           `json:"passes"`
	// Rested は投稿を休んだ人数、Arrived と Departed はその日にほかのリージョンから移って
	// きた人数と、ほかのリージョンへ移った人数。
	Rested   int     `json:"rested"`
	Arrived  int     `json:"arrived"`
	Departed int     `json:"departed"`
	Ops      int64   `json:"ops"`
	Failures int64   `json:"failures"`
	FailRate float64 `json:"failRate"`
	// Points はその日にそのリージョンの利用者が得た得点。
	Points int64 `json:"points"`
}

// book はリージョンと日ごとの集計。
type book struct {
	tt    timetable
	start time.Time

	mu   sync.Mutex
	days map[string][]*dayStats
}

func newBook(tt timetable, start time.Time) *book {
	b := &book{tt: tt, start: start, days: map[string][]*dayStats{}}
	for _, region := range geo.Regions {
		for range tt.days() {
			b.days[region] = append(b.days[region], &dayStats{passers: map[string]time.Duration{}, excused: map[string]bool{}, departed: map[string]bool{}})
		}
	}
	return b
}

func (b *book) stats(region string, day int) *dayStats {
	if day < 1 || day > len(b.days[region]) {
		return nil
	}
	return b.days[region][day-1]
}

// observe は成長の判定に数える操作の結果を、操作が始まった日に数える。
func (b *book) observe(record client.Record) {
	region := geo.RegionOf(record.Country)
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats(region, b.tt.dayAt(region, record.Start.Sub(b.start)))
	if s == nil {
		return
	}
	s.ops++
	if record.Err != nil && !record.Allowed {
		s.failures++
	}
}

// fail は応答の検証に失敗した操作を 1 件の失敗として数える。
// 間引いた検証で見つけた失敗は、打ち切りの判定と同じく n 件（割合の逆数）として数える。
func (b *book) fail(region string, at time.Time, n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.stats(region, b.tt.dayAt(region, at.Sub(b.start))); s != nil {
		s.failures += n
	}
}

// excuse は userID が region の day 日目に自分で投稿を休んだことを記録する。
func (b *book) excuse(region string, day int, userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.stats(region, day); s != nil && !s.excused[userID] {
		s.excused[userID] = true
		s.rested++
	}
}

// depart は userID が region の今の日のうちにほかのリージョンへ移ったことを記録する。
func (b *book) depart(region, userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.stats(region, b.tt.dayAt(region, time.Since(b.start))); s != nil {
		s.departed[userID] = true
	}
}

// arrive は userID がほかのリージョンから region に移ってきたことを、region の今の日に
// 記録する。その日はよそで過ごしたので、通過できなかった人には数えない。
func (b *book) arrive(region, userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.stats(region, b.tt.dayAt(region, time.Since(b.start))); s != nil {
		if !s.excused[userID] {
			s.excused[userID] = true
			s.arrived++
		}
		delete(s.departed, userID)
	}
}

// pass は userID が region の day 日目に、投稿を始めてから took でゲートを通過したことを
// 記録する。初めてなら true。
func (b *book) pass(region string, day int, userID string, took time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats(region, day)
	if s == nil {
		return false
	}
	if _, ok := s.passers[userID]; ok {
		return false
	}
	s.passers[userID] = took
	return true
}

// decide は region の day 日目が始まるときに、新しく来るユーザー数を決める。population は
// 今のユーザー数（前日のあいだにいた人。前日に来て登録中の人を含む）。
func (b *book) decide(c GrowthConfig, region string, day, population int) *dayStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats(region, day)
	s.population = population
	prev := b.stats(region, day-1)
	if prev == nil {
		return s
	}
	s.failRate = float64(prev.failures) / float64(max(prev.ops, c.MinOps))
	s.healthy = s.failRate <= c.MaxFailRate
	quickWithin := time.Duration(float64(b.tt.period) * c.QuickShare)
	s.quick, s.newcomers, s.invites = 0, 0, 0
	for _, took := range prev.passers {
		if took <= quickWithin {
			s.quick++
		}
		s.invites += c.invites(took, b.tt.period)
	}
	// 前日にいて、通過したか通過しなくてよかった人。ほかのリージョンへ移った人は、
	// 今の人数に入っていないので除く。
	accounted := 0
	for id := range prev.passers {
		if !prev.departed[id] {
			accounted++
		}
	}
	for id := range prev.excused {
		if _, ok := prev.passers[id]; !ok && !prev.departed[id] {
			accounted++
		}
	}
	s.missed = max(population-accounted, 0)
	if s.healthy && !b.tt.isGrandPrix(day) {
		s.newcomers = max(int(math.Ceil(s.invites-float64(s.missed)*c.Discourage)), 0)
	}
	return s
}

func (b *book) report() []DayReport {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []DayReport
	for _, region := range geo.Regions {
		for i, s := range b.days[region] {
			took := slices.Sorted(maps.Values(s.passers))
			out = append(out, DayReport{
				Region: region, Day: i + 1, Population: s.population, Newcomers: s.newcomers, Quick: s.quick, Missed: s.missed, Invites: s.invites,
				Passes: len(s.passers), Rested: s.rested, Arrived: s.arrived, Departed: len(s.departed), GateP50: percentileOf(took, 0.5), GateP90: percentileOf(took, 0.9),
				Ops: s.ops, Failures: s.failures, FailRate: min(1, float64(s.failures)/float64(max(s.ops, 1))),
			})
		}
	}
	return out
}

// passes はグランプリの日（全リージョン）と、それ以外の日にゲートを通過した人数。
func (b *book) passes() (days, grandPrix map[string]int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	days, grandPrix = map[string]int{}, map[string]int{}
	for region, list := range b.days {
		for i, s := range list {
			if b.tt.isGrandPrix(i + 1) {
				grandPrix[region] += len(s.passers)
			} else {
				days[region] += len(s.passers)
			}
		}
	}
	return days, grandPrix
}

// accounting はユーザーの操作をどの集計に数えるか（三面会計）。正しさの違反はどの
// ユーザーでも失格にする。slo は打ち切りの判定（5 秒ごとのエラー率）、growth は成長の
// 判定（前日の失敗率）に数える。BAN による拒否はどちらにも数えない。
type accounting struct {
	r      *Runner
	slo    bool
	growth bool
}

func (a *accounting) Observe(record client.Record) {
	if record.Status == 403 && record.Code == "ip_banned" {
		return
	}
	if rec := a.r.recorder.Load(); a.slo && rec != nil {
		rec.Observe(record)
	}
	if b := a.r.books.Load(); a.growth && b != nil {
		b.observe(record)
	}
}

func (a *accounting) Begin(route, country string, start time.Time) func() {
	if rec := a.r.recorder.Load(); a.slo && rec != nil {
		return rec.Begin(route, country, start)
	}
	return func() {}
}

// openDay は region の day 日目の投稿期間を開く。2 日目以降は、前日の結果から新しく来る
// ユーザー数を決めて迎え入れ、前日に最も反応を集めたユーザーを認定する。
func (r *Runner) openDay(ctx context.Context, region string, day int) (string, error) {
	date := r.firstDate.AddDate(0, 0, day-1)
	open := r.model.BeginOpenWindow(region)
	sent := time.Now()
	// 開く要求は 1 日より長くは待たない。その日を丸ごと失わないよう、結果のわからない
	// 失敗（タイムアウトや 5xx）のあとは、地域のユーザーとして今の投稿期間を読み、
	// 開けていればそれを使う。
	openCtx, cancel := context.WithTimeout(ctx, max(r.tt.period*8/9, time.Second))
	window, err := r.admin.OpenWindow(openCtx, r.adminToken, region, date.Format(time.DateOnly))
	cancel()
	if err != nil && !isRejection(err) && ctx.Err() == nil {
		if recovered, ok := r.recoverWindow(ctx, region, day, date.Format(time.DateOnly)); ok {
			// 回復できたことは運営向けにだけ残し、参加者向けの Story には出さない。
			r.operatorLogger().Info(fmt.Sprintf("%s %s: 投稿期間を開く要求に応答がなく、%.1f 秒後に開いていることを確かめました", region, dayLabel(r.tt, day), time.Since(sent).Seconds()))
			window, err = recovered, nil
		}
	}
	took := time.Since(sent)
	if err != nil {
		// 開けたかどうかわからない（タイムアウトなど）ので、モデルでは不明のままにする。
		settle(open, err)
		r.adminError(ctx, "%s の投稿期間を開けません: %v", region, err)
		return "", nil
	}
	if window.ID == "" || window.Region != region || window.LocalDate != date.Format(time.DateOnly) {
		open.Unknown()
		return "", fmt.Errorf("%s の投稿期間を開いた応答が不正です: %+v", region, window)
	}
	r.mu.Lock()
	r.windowDay[window.ID] = day
	r.openedAt[window.ID] = sent
	previous := r.lastWindow[region]
	r.lastWindow[region] = window.ID
	r.opened[region]++
	r.mu.Unlock()
	r.model.OpenedWindow(open, model.Window{ID: window.ID, Region: region, LocalDate: date})

	stats := r.books.Load().decide(r.cfg.Growth, region, day, r.population(region))
	r.tell(region, day, stats, took)
	if stats.newcomers > 0 {
		r.admit(ctx, region, day, stats.newcomers)
	}
	if previous != "" {
		r.certifyTop(ctx, region, previous)
	}
	return window.ID, nil
}

// recoverWindow は、開く要求の結果がわからないとき、region のユーザーとして今の投稿期間を
// 次の日の始まりまで読み、date の投稿期間が開いていれば返す。読み取りも含めて次の日の
// 始まりで打ち切るので、アプリが応答しなくても次の日を遅らせない。
func (r *Runner) recoverWindow(ctx context.Context, region string, day int, date string) (client.Window, bool) {
	b := r.books.Load()
	if b == nil {
		return client.Window{}, false
	}
	ctx, cancel := context.WithDeadline(ctx, b.start.Add(r.tt.end(region, day)))
	defer cancel()
	// ユーザーはリージョンを移ると u.API を差し替えるので、ロックの中で取っておく。
	type reader struct {
		api   *client.Client
		token string
	}
	var candidates []reader
	r.mu.Lock()
	for _, u := range r.users[region] {
		if u.Token != "" && !u.stopped.Load() && !u.returning {
			candidates = append(candidates, reader{api: u.API.WithObserver(nil), token: u.Token})
		}
	}
	r.mu.Unlock()
	for i := 0; i < len(candidates) && ctx.Err() == nil; {
		c := candidates[i]
		status, err := c.api.CurrentWindow(ctx, c.token)
		switch {
		case isStatus(err, http.StatusUnauthorized):
			// セッションが切れていれば次のユーザーで読む。ほかの失敗は同じユーザーで待って読み直す。
			i++
			continue
		case err == nil:
			if w := status.Window; w != nil && w.Region == region && w.LocalDate == date && !r.model.HasWindow(w.ID) {
				return *w, true
			}
		}
		if sleepUntil(ctx, time.Now().Add(300*time.Millisecond)) != nil {
			break
		}
	}
	return client.Window{}, false
}

// isRejection は err が、書き込みが確実に反映されなかったことを表す 4xx か。
func isRejection(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500
}

// admit は count 人の新しいユーザーを、その日の始めの 4 割の時間に均等に散らして登録する。
// まとめて登録すると、その瞬間だけ接続やロックが集中する。
func (r *Runner) admit(ctx context.Context, region string, day, count int) {
	spread := r.tt.period * 2 / 5
	start := time.Now()
	r.mu.Lock()
	r.arriving[region] += count
	r.mu.Unlock()
	for i := range count {
		at := start.Add(spread * time.Duration(i) / time.Duration(count))
		r.wg.Go(func() {
			if sleepUntil(ctx, at) == nil {
				r.runNewcomer(ctx, region)
			} else {
				r.arrived(region)
			}
		})
	}
}

// arrived は新しいユーザーの登録が終わった（か、諦めた）ことを記録する。
func (r *Runner) arrived(region string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arriving[region]--
}

// runNewcomer は走行の途中で来たユーザー。登録したら関係を作り、開いている投稿期間に
// そのまま投稿してから、ほかのユーザーと同じループに入る。
func (r *Runner) runNewcomer(ctx context.Context, region string) {
	username, displayName := r.newName()
	a := agent.New(r.api, region, int(hashOf(r.seed, "country", username)%3))
	a.Username = username
	u := r.newUser(a, activityOf(r.seed, username))
	u.registered = true
	u.API = u.API.WithObserver(&accounting{r: r, slo: true, growth: true})
	err := u.Register(ctx, username, displayName)
	if err != nil {
		// 登録できなかった人は r.users に入らず、走行の終わりに接続を閉じられないので、ここで閉じる。
		u.API.CloseIdleConnections()
		r.arrived(region)
		r.requestError(ctx, u, "%s: 新しいユーザーを登録できません: %v", username, err)
		return
	}
	r.model.AddUser(model.User{ID: u.ID, Username: u.Username, SecretKey: u.SecretKey, Region: region})
	r.addUser(u)
	r.arrived(region)
	// 登録した人の一部は、まずプロフィール画像と自己紹介を設定する。
	if unit(r.seed, "onboard-avatar", username) < u.persona.avatar {
		r.changeProfileImage(ctx, u)
	}
	if unit(r.seed, "onboard-bio", username) < u.persona.bio {
		r.updateBio(ctx, u)
	}
	// 友達つながりで来た人は登録してすぐ何人かに申請し、残りは毎日の投稿のあとで少しずつ
	// 送る。認定公開アカウントをきっかけに来た人も、あとから少しは友達を作る。
	if u.rng.Float64() < r.cfg.Growth.FriendRatio {
		u.circleLeft = u.persona.circle
		r.reachOut(ctx, u, reachOutOnRegister)
	} else {
		u.circleLeft = u.persona.circle / 3
		r.followCertified(ctx, u, 1+u.rng.IntN(3))
	}
	r.runUser(ctx, u, true, nil)
}

// certifyTop は前日の投稿期間 windowID で最も反応（RealMoji とコメント）を集めた、まだ
// 認定されていないユーザーを管理者が認定する。認定されたユーザーは翌日から public で
// 投稿し、新しいユーザーにフォローされる。
func (r *Runner) certifyTop(ctx context.Context, region, windowID string) {
	r.mu.Lock()
	counts := r.reactionsByWindow[windowID]
	var best *user
	bestCount := 0
	for id, n := range counts {
		u := r.byID[id]
		// 初期データのユーザーは認定しない（アカウントの種類は履歴の検証で比べる）。退会する
		// 予定のユーザーも認定しない。
		if u == nil || !u.registered || u.leaveOn > 0 || u.Region != region || u.certified.Load() || u.stopped.Load() {
			continue
		}
		if n > bestCount || (n == bestCount && best != nil && u.ID < best.ID) {
			best, bestCount = u, n
		}
	}
	r.mu.Unlock()
	if best == nil || bestCount < 2 {
		return
	}
	if err := r.admin.CertifyUser(ctx, r.adminToken, best.ID); err != nil {
		r.adminError(ctx, "%s を認定できません: %v", best.Username, err)
		return
	}
	r.certify(best)
	// 誰が認定されたかは DB のアカウント種別で分かるので、ログには出さない。
}

func (r *Runner) certify(u *user) {
	u.certified.Store(true)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.certified, u.ID) {
		r.certified = append(r.certified, u.ID)
	}
}

func (r *Runner) certifiedSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.certified)
}

// population は region の動いている負荷ユーザーの数（登録中の新しいユーザーを含む）。
func (r *Runner) population(region string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.arriving[region]
	for _, u := range r.users[region] {
		// BAN されたユーザーは通過できないので、使えなかった人として数え続ける。正常な
		// ユーザーを BAN して、成長の判定から外すことはできない。
		if !u.stopped.Load() || u.banned.Load() || u.stranded.Load() {
			n++
		}
	}
	return n
}

func (r *Runner) addUser(u *user) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[u.Region] = append(r.users[u.Region], u)
	r.byID[u.ID] = u
}

func (r *Runner) usersSnapshot() []*user {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*user
	for _, region := range geo.Regions {
		out = append(out, r.users[region]...)
	}
	return out
}

// newName は初期データのユーザーと同じ形の、まだ使っていないユーザー名と表示名を返す。
func (r *Runner) newName() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		r.registrations++
		username := dataset.Username(r.nameRand, len(r.dataset.Users)+r.registrations*7+r.nameRand.IntN(7))
		if !r.usernames[username] {
			r.usernames[username] = true
			return username, dataset.DisplayName(r.nameRand)
		}
	}
}

// percentileOf は昇順に並んだ sorted の q 分位点（空なら 0）。
func percentileOf[T any](sorted []T, q float64) T {
	if len(sorted) == 0 {
		var zero T
		return zero
	}
	return sorted[min(int(float64(len(sorted))*q), len(sorted)-1)]
}
