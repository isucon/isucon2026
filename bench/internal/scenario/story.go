package scenario

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

// 走行の様子を参加者に伝える。日ごとのユーザー数は「asia 1 日目: 19 人」の形で出すだけに
// する。増えなかった理由（失敗率、通過できなかった人数など）は、運営向けの結果
// （DayReport）に残す。

// tell は region の day 日目の始まりに、前日の結果と今日の人数を記録する。
// slowOpen は、投稿期間を開くのにかかった時間を運営向けに記録する下限。
// 参加者向けの Story には書き添えない。
const slowOpen = time.Second

// slowInitialize は、初期化にかかった時間を運営向けに記録する下限。
// 参加者向けの Story には残さない。
const slowInitialize = 10 * time.Second

func (r *Runner) tell(region string, day int, s *dayStats, openTook time.Duration) {
	// その日のユーザー数（始まりの人数＋その日に来る新しいユーザー）だけを出す。
	// 投稿期間を開くのに時間がかかっても、参加者向けには書き添えない（運営向けにだけ残す）。
	if openTook >= slowOpen {
		r.operatorLogger().Info("投稿期間を開くのに時間がかかりました", "region", region, "day", dayLabel(r.tt, day), "elapsed", openTook.Round(time.Millisecond))
	}
	line := fmt.Sprintf("%s %s: %d 人", region, dayLabel(r.tt, day), s.population+s.newcomers)
	r.cfg.Logger.Info(line)
	r.mu.Lock()
	r.story = append(r.story, line)
	r.mu.Unlock()
}

// tellGrandPrix は走行の終わりに、グランプリに参加できた人数を記録する。
func (r *Runner) tellGrandPrix() {
	b := r.books.Load()
	if b == nil {
		return
	}
	_, grandPrix := b.passes()
	// グランプリは地域どうしが競うものではないので、合計は出さず地域ごとの人数だけを出す。
	names := map[string]string{geo.Asia: "アジア", geo.Europe: "ヨーロッパ", geo.America: "アメリカ"}
	var parts []string
	for _, region := range geo.Regions {
		parts = append(parts, fmt.Sprintf("%s %d 人", names[region], grandPrix[region]))
	}
	line := fmt.Sprintf("グランプリ: %s", strings.Join(parts, ", "))
	r.cfg.Logger.Info(line)
	r.mu.Lock()
	r.story = append(r.story, line)
	r.mu.Unlock()
}

func dayLabel(tt timetable, day int) string {
	if tt.isGrandPrix(day) {
		return "最終日（グランプリ）"
	}
	return fmt.Sprintf("%d 日目", day)
}

// reputation は正常なユーザーが BAN されたことを悪評として記録する。
func (r *Runner) reputation(u *user) {
	line := fmt.Sprintf("正常なユーザー %s（%s）が BAN されて使えなくなり、悪評が立ちました", u.Username, u.API.ClientIP())
	r.cfg.Logger.Warn(line)
	r.mu.Lock()
	r.reputationCount++
	if r.reputationCount <= 5 {
		r.story = append(r.story, line)
	}
	r.mu.Unlock()
}

func (r *Runner) reputations() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reputationCount
}

// storyLines は走行の様子。リージョンの順、日の順に並べる。
func (r *Runner) storyLines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := slices.Clone(r.story)
	order := func(line string) int {
		for i, region := range geo.Regions {
			if strings.HasPrefix(line, region+" ") {
				return i
			}
		}
		return len(geo.Regions)
	}
	slices.SortStableFunc(lines, func(a, b string) int { return order(a) - order(b) })
	return lines
}
