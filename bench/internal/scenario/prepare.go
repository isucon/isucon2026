package scenario

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/dataset"
)

// initialRegistrations は region に最初に登録するユーザーと認定公開アカウントの数。
// サービスはアジアで広まっていて、海外はまだ少ない（-users の 1/2）。初期実装の登録は
// テーブル全体をロックし、海外のサーバーは DB まで遠いので、海外の登録は全リージョンで
// 1 件ずつしか進まない。人数は走行中に増える。
func (r *Runner) initialRegistrations(region string) (users, influencers int) {
	if region == geo.Asia {
		return r.cfg.Users, 2
	}
	return max(1, r.cfg.Users/2), 1
}

// prepareUsers は負荷ユーザーを用意する。
//
// 各リージョンに -users 人（海外はその 1/4）を登録し、リージョンごとに 1 列に並べて隣どうしを友達にする
// （列の 2 つ先は友達の友達になる）。4 人ごとに 3 つ先のユーザーをブロックする。海外と
// asia の友達関係は、走行中に来る海外のユーザーが asia のユーザーに申請して作る。
// 各リージョンに認定公開アカウントを登録して管理者が認定し、bench が登録したユーザーは
// 同じリージョンの 1 件をフォローする。初期データの cohort のユーザーにも -users 人まで
// ログインし、負荷ユーザーに加える（初期データの関係をそのまま持つ）。
func (r *Runner) prepareUsers(ctx context.Context) string {
	r.step("負荷ユーザーの準備")
	r.cfg.Logger.Info("phase: prepare users")
	started := time.Now()
	lap := func(name string) {
		r.step("負荷ユーザーの準備: " + name)
		r.cfg.Logger.Info("prepare users", "step", name, "elapsed", time.Since(started).Round(time.Millisecond))
	}
	type plan struct {
		u          *user
		influencer bool
	}
	var plans []plan
	for _, region := range geo.Regions {
		users, influencers := r.initialRegistrations(region)
		for i := range users + influencers {
			username, _ := r.newName()
			a := agent.New(r.api, region, i)
			a.Username = username
			u := r.newUser(a, activityOf(r.seed, username))
			u.registered = true
			influencer := i >= users
			if influencer {
				u.activity = browser
			}
			plans = append(plans, plan{u: u, influencer: influencer})
		}
	}
	// 登録と cohort のログインは互いに関係しないので並行に行う。初期実装の登録はテーブル
	// 全体をロックするので 1 件ずつ進むが、ログインはその間も進む。
	failures := make([]error, len(plans))
	var cohortReason string
	parallel(2, func(i int) {
		if i == 1 {
			cohortReason = r.loginCohort(ctx)
			return
		}
		parallel(len(plans), func(i int) {
			u := plans[i].u
			failures[i] = u.Register(ctx, u.Username, dataset.DisplayName(u.rng))
		})
	})
	if err := errors.Join(failures...); err != nil {
		return fmt.Sprintf("負荷ユーザーの登録に失敗しました: %v", err)
	}
	if cohortReason != "" {
		return cohortReason
	}
	lap("登録と cohort のログイン")
	byRegion := map[string][]*user{}
	var influencers []*user
	for _, p := range plans {
		r.model.AddUser(model.User{ID: p.u.ID, Username: p.u.Username, SecretKey: p.u.SecretKey, Region: p.u.Region})
		if p.influencer {
			influencers = append(influencers, p.u)
		} else {
			byRegion[p.u.Region] = append(byRegion[p.u.Region], p.u)
		}
		r.addUser(p.u)
	}
	// 関係はまず地域の中でできる。リージョンどうしと、認定とフォローは並行に作る。
	var reasons [4]string
	parallel(len(reasons), func(i int) {
		if i < len(geo.Regions) {
			reasons[i] = r.connect(ctx, byRegion[geo.Regions[i]])
			return
		}
		for _, u := range influencers {
			if err := r.admin.CertifyUser(ctx, r.adminToken, u.ID); err != nil {
				reasons[i] = fmt.Sprintf("認定公開アカウントを認定できません: %v", err)
				return
			}
			r.certify(u)
		}
		// bench が登録したユーザーは、同じリージョンの認定公開アカウントを 1 件フォローする。
		// 初期データのユーザーは初期データのフォローをそのまま持つ。
		var followers []*user
		for _, p := range plans {
			if !p.influencer {
				followers = append(followers, p.u)
			}
		}
		parallel(len(followers), func(i int) {
			u := followers[i]
			var mine []*user
			for _, c := range influencers {
				if c.Region == u.Region {
					mine = append(mine, c)
				}
			}
			if len(mine) == 0 {
				return
			}
			target := mine[i%len(mine)]
			w := r.model.BeginFollow(u.ID, target.ID, true)
			err := u.API.Follow(ctx, u.Token, target.ID)
			settle(w, err)
			if err != nil {
				r.board.Errorf("%s: 認定公開アカウントをフォローできません: %v", u.Username, err)
				return
			}
			u.follows[target.ID] = true
		})
	})
	for _, reason := range reasons {
		if reason != "" {
			return reason
		}
	}
	lap("友達と認定とフォロー")
	return ""
}

// loginCohort は初期データの cohort のユーザー（負荷走行で書き込んでよい）に、リージョン
// ごとに -users 人までログインし、負荷ユーザーに加える。初期データの関係（履歴のユーザー
// との友達関係）をそのまま持つ。
func (r *Runner) loginCohort(ctx context.Context) string {
	var cohort []*user
	for _, region := range geo.Regions {
		for i, du := range r.initial.CohortUsers(region) {
			if i >= r.cfg.Users {
				break
			}
			a := agent.New(r.api, region, i)
			a.ID, a.Username, a.SecretKey = du.ID, du.Username, du.SecretKey
			u := r.newUser(a, cohortActivity(r.seed, du.Username, du.Activity))
			u.cohort = true
			cohort = append(cohort, u)
		}
	}
	failures := make([]error, len(cohort))
	parallel(len(cohort), func(i int) { failures[i] = cohort[i].Login(ctx) })
	if err := errors.Join(failures...); err != nil {
		return fmt.Sprintf("初期データのユーザーとしてログインできません: %v", err)
	}
	for _, u := range cohort {
		r.addUser(u)
	}
	return ""
}

// connect は users の間に友達関係とブロックを作る。関係を作るトランザクションは 2 人の
// ユーザーの行をロックするので、同じユーザーが同時に 2 つの組に入ってロックの待ちが
// 連鎖しないよう、偶数番目の組、奇数番目の組、ブロックの順に分けて並行に作る。
func (r *Runner) connect(ctx context.Context, users []*user) string {
	for _, first := range []int{0, 1} {
		var pairs [][2]*user
		for i := first; i+1 < len(users); i += 2 {
			pairs = append(pairs, [2]*user{users[i], users[i+1]})
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
	var blocks [][2]*user
	for i := 0; i+3 < len(users); i += 4 {
		blocks = append(blocks, [2]*user{users[i], users[i+3]})
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
func (r *Runner) befriend(ctx context.Context, a, b *user) error {
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
