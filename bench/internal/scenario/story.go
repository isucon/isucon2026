package scenario

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

// 走行の様子を参加者に伝える。スコアの内訳や閾値の数値は出さず、日ごとに何人いて、何人が
// 投稿してフィードを見られて、何人が新しく来たかを文で出す。ユーザーが増えなかった日は
// その理由（失敗が多かった）を出すので、参加者は次に何を調べるかを決められる。

// tell は region の day 日目の始まりに、前日の結果と今日の人数を記録する。
// slowOpen は、投稿期間を開くのにかかった時間を物語に書き添える下限。
const slowOpen = time.Second

// slowInitialize は、初期化にかかった時間を物語に残す下限。
const slowInitialize = 10 * time.Second

// record は 1 行を物語とログに残す。
func (r *Runner) record(line string) {
	r.cfg.Logger.Info(line)
	r.mu.Lock()
	r.story = append(r.story, line)
	r.mu.Unlock()
}

func (r *Runner) tell(region string, day int, s *dayStats, openTook time.Duration) {
	var line string
	label := dayLabel(r.tt, day)
	switch {
	case day == 1:
		line = fmt.Sprintf("%s %s: %d 人で始まりました", region, label, s.population)
	case r.tt.isGrandPrix(day):
		line = fmt.Sprintf("%s %s: 今いる %d 人で競います", region, label, s.population)
	case s.healthy && s.newcomers > 0 && s.missed > 0:
		line = fmt.Sprintf("%s %s: 前日にサービスを快適に使えた人が友達を誘い、%d 人が新しく来ました（%d 人 → %d 人、使えなかった %d 人の不満で一部は思いとどまりました）", region, label, s.newcomers, s.population, s.population+s.newcomers, s.missed)
	case s.healthy && s.newcomers > 0:
		line = fmt.Sprintf("%s %s: 前日にサービスを快適に使えた人が友達を誘い、%d 人が新しく来ました（%d 人 → %d 人）", region, label, s.newcomers, s.population, s.population+s.newcomers)
	case s.healthy && s.invites > 0:
		line = fmt.Sprintf("%s %s: 前日にサービスを使えなかった %d 人の不満が広まり、新しい人は来ませんでした（%d 人）", region, label, s.missed, s.population)
	case s.healthy && s.missed < s.population:
		line = fmt.Sprintf("%s %s: 前日はサービスが遅く、友達を誘う人はいませんでした（%d 人）", region, label, s.population)
	case s.healthy:
		line = fmt.Sprintf("%s %s: 前日に投稿してフィードを見られた人がいなかったので、新しい人は来ませんでした（%d 人）", region, label, s.population)
	default:
		line = fmt.Sprintf("%s %s: 前日はエラーが多く（%.1f%%）、新しい人は来ませんでした（%d 人）", region, label, s.failRate*100, s.population)
	}
	if openTook >= slowOpen {
		line += fmt.Sprintf("。投稿期間を開くのに %.1f 秒かかり、その分だけ通知が遅れました", openTook.Seconds())
	}
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
	var parts []string
	total := 0
	for _, region := range geo.Regions {
		parts = append(parts, fmt.Sprintf("%s %d 人", region, grandPrix[region]))
		total += grandPrix[region]
	}
	line := fmt.Sprintf("グランプリ（全リージョン同時）に参加できたのは %d 人でした（%s）", total, strings.Join(parts, "、"))
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
