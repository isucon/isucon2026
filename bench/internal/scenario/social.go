package scenario

import (
	"context"
	"errors"
	"fmt"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// initialUsers は region の最初の負荷ユーザー数。サービスはアジアで広まっていて、海外の
// ユーザーは Buzz（RatchetConfig.BuzzAt）まで少ない。
func (r *Runner) initialUsers(region string) int {
	if region == geo.Asia {
		return r.cfg.Users
	}
	return max(1, r.cfg.Users/8)
}

// prepareUsers は負荷ユーザーを並行に登録し、ユーザー同士の関係を作る。
//
// リージョンごとにユーザーを 1 列に並べ、隣どうしを友達にする（列の 2 つ先は友達の友達になる）。
// 4 人ごとに、3 つ先のユーザーをブロックする（友達でも友達の友達でもない相手）。関係はまず
// 地域の中でできる。
func (r *Runner) prepareUsers(ctx context.Context) string {
	r.cfg.Logger.Info("phase: prepare users")
	var users []*agent.Agent
	var names []string
	for _, region := range geo.Regions {
		for index := range r.initialUsers(region) {
			users = append(users, agent.New(r.api, region, index))
			names = append(names, fmt.Sprintf("bench_%s_%03d", region, index))
		}
	}
	failures := make([]error, len(users))
	parallel(len(users), func(i int) {
		failures[i] = users[i].Register(ctx, names[i], names[i])
	})
	if err := errors.Join(failures...); err != nil {
		return fmt.Sprintf("負荷ユーザーの登録に失敗しました: %v", err)
	}
	for _, user := range users {
		r.model.AddUser(model.User{ID: user.ID, Username: user.Username, SecretKey: user.SecretKey, Region: user.Region})
		r.agents[user.Region] = append(r.agents[user.Region], user)
	}
	// 関係はまず地域の中でできる。
	for _, region := range geo.Regions {
		if reason := r.connect(ctx, r.agents[region]); reason != "" {
			return reason
		}
	}
	return r.loginCohort(ctx)
}

// loginCohort は初期データの cohort のユーザー（負荷走行で書き込んでよい）に、リージョン
// ごとに最初の負荷ユーザー数（initialUsers）までログインし、負荷ユーザーに加える。初期データの関係（履歴のユーザーとの
// 友達関係）をそのまま持つ。
func (r *Runner) loginCohort(ctx context.Context) string {
	var cohort []*agent.Agent
	for _, region := range geo.Regions {
		for i, u := range r.initial.CohortUsers(region) {
			if i >= r.initialUsers(region) {
				break
			}
			a := agent.New(r.api, region, i)
			a.ID, a.Username, a.SecretKey = u.ID, u.Username, u.SecretKey
			cohort = append(cohort, a)
		}
	}
	failures := make([]error, len(cohort))
	parallel(len(cohort), func(i int) { failures[i] = cohort[i].Login(ctx) })
	if err := errors.Join(failures...); err != nil {
		return fmt.Sprintf("初期データのユーザーとしてログインできません: %v", err)
	}
	for _, a := range cohort {
		r.cohort[a.ID] = true
		r.agents[a.Region] = append(r.agents[a.Region], a)
	}
	return ""
}

// connect は users の間に友達関係とブロックを作る。関係を作るトランザクションは 2 人の
// ユーザーの行をロックするので、同じユーザーが同時に 2 つの組に入ってロックの待ちが
// 連鎖しないよう、偶数番目の組、奇数番目の組、ブロックの順に分けて並行に作る。
func (r *Runner) connect(ctx context.Context, users []*agent.Agent) string {
	for _, first := range []int{0, 1} {
		var pairs [][2]*agent.Agent
		for i := first; i+1 < len(users); i += 2 {
			pairs = append(pairs, [2]*agent.Agent{users[i], users[i+1]})
		}
		failures := make([]error, len(pairs))
		parallel(len(pairs), func(i int) {
			if failures[i] = r.befriend(ctx, pairs[i][0], pairs[i][1]); failures[i] == nil {
				r.addFriendship(pairs[i][0].ID, pairs[i][1].ID)
			}
		})
		if err := errors.Join(failures...); err != nil {
			return fmt.Sprintf("負荷ユーザー同士を友達にできません: %v", err)
		}
	}
	var blocks [][2]*agent.Agent
	for i := 0; i+3 < len(users); i += 4 {
		blocks = append(blocks, [2]*agent.Agent{users[i], users[i+3]})
	}
	failures := make([]error, len(blocks))
	parallel(len(blocks), func(i int) {
		blocker, blocked := blocks[i][0], blocks[i][1]
		w := r.model.BeginBlock(blocker.ID, blocked.ID)
		failures[i] = blocker.API.Block(ctx, blocker.Token, blocked.ID)
		settle(w, failures[i])
	})
	if err := errors.Join(failures...); err != nil {
		return fmt.Sprintf("負荷ユーザーをブロックできません: %v", err)
	}
	return ""
}

// befriend は a から b へ友達申請を送り、b が承認する。
func (r *Runner) befriend(ctx context.Context, a, b *agent.Agent) error {
	request, err := a.API.CreateFriendRequest(ctx, a.Token, b.ID)
	if err != nil {
		return err
	}
	if request.Status != "pending" {
		return fmt.Errorf("%s から %s への友達申請の状態が %s です", a.Username, b.Username, request.Status)
	}
	w := r.model.BeginFriendship(a.ID, b.ID, true)
	accepted, err := b.API.TransitionFriendRequest(ctx, b.Token, request.RelationID, "accept")
	settle(w, err)
	if err != nil {
		return err
	}
	if accepted.Status != "accepted" {
		return fmt.Errorf("承認した友達申請の状態が %s です", accepted.Status)
	}
	return nil
}
