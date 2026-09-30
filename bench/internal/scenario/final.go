package scenario

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
)

// finalCheckTimeout は走行後の整合性チェックにかける時間の上限。
const finalCheckTimeout = 30 * time.Second

// finalCheckUsers は走行後の整合性チェックで確かめる負荷ユーザーの数の上限、
// finalCheckConcurrency は同時に確かめる数。速いアプリほど負荷ユーザーが増えるので、
// 全員を一斉に確かめると、走行中の負荷と関係なく最後に詰まる。書き込みの取りこぼしは
// 系統的に起きるので、無作為に選んだ一部で見つかる。
const (
	finalCheckUsers       = 300
	finalCheckConcurrency = 64
)

// finalCheck は負荷走行のあと、無作為に選んだ負荷ユーザー（finalCheckUsers 人まで）について、
// 自分の投稿（負荷走行で開いた投稿期間の分）と友達の一覧を読み、受け付けられた書き込みと
// 突き合わせる。受け付けられた
// 投稿や友達関係が失われていたり、削除した投稿や友達でない人が残っていたりすれば fail とする。
// 結果のわからない書き込みは、反映されていてもいなくてもよい。
func (r *Runner) finalCheck(ctx context.Context) string {
	r.cfg.Logger.Info("phase: final check")
	ctx, cancel := context.WithTimeout(ctx, finalCheckTimeout)
	defer cancel()
	start := time.Now()
	defer func() { r.cfg.Logger.Info("final check done", "elapsed", time.Since(start).Round(time.Millisecond)) }()
	users := r.agentsSnapshot()
	rand.Shuffle(len(users), func(i, j int) { users[i], users[j] = users[j], users[i] })
	users = users[:min(len(users), finalCheckUsers)]
	months := r.model.LoadMonths()
	failures := make([]error, len(users))
	slots := make(chan struct{}, finalCheckConcurrency)
	parallel(len(users), func(i int) {
		slots <- struct{}{}
		defer func() { <-slots }()
		// 走行後のリクエストは負荷走行の計測に含めない。
		user := *users[i]
		user.API = user.API.WithObserver(nil)
		failures[i] = r.finalCheckUser(ctx, &user, months)
	})
	if err := errors.Join(failures...); err != nil {
		return "走行後の整合性チェックに失敗しました: " + err.Error()
	}
	return ""
}

func (r *Runner) finalCheckUser(ctx context.Context, user *agent.Agent, months []time.Time) error {
	sent := time.Now()
	var posts []string
	for _, month := range months {
		var before *client.Post
		for {
			page, err := user.API.MemoriesMonth(ctx, user.Token, month.Format("2006-01"), before)
			if err != nil {
				return fmt.Errorf("%s: Memories を取得できません: %w", user.Username, err)
			}
			for _, item := range page.Items {
				if r.model.HasWindow(item.PostingWindowID) {
					posts = append(posts, item.ID)
				}
			}
			if !page.HasMore || len(page.Items) == 0 {
				break
			}
			before = &page.Items[len(page.Items)-1].Post
		}
	}
	var friends []string
	var before *client.Relationship
	for {
		page, err := user.API.Relationships(ctx, user.Token, "friends", before)
		if err != nil {
			return fmt.Errorf("%s: 友達の一覧を取得できません: %w", user.Username, err)
		}
		for _, item := range page.Items {
			friends = append(friends, item.User.ID)
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1]
	}
	read := r.model.ReadBetween(sent, time.Now())
	violations := append(r.model.CheckOwnPosts(user.ID, posts, read), r.model.CheckFriends(user.ID, friends, read)...)
	if len(violations) > 0 {
		return fmt.Errorf("%s: %s", user.Username, violations[0])
	}
	return nil
}
