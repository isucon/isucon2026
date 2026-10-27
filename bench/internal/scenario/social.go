package scenario

import (
	"context"
	"crypto/sha256"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// friendRequestsPerNewcomer は友達つながりで来たユーザーが送る友達申請の数。
const friendRequestsPerNewcomer = 3

// social は友達まわりの操作を 1 つ行う。検索して申請する、届いた申請を断る、送った申請を
// 取り下げる、関係の一覧を見る、まれに友達を解除する。
func (r *Runner) social(ctx context.Context, u *user, s *session) {
	switch u.rng.IntN(6) {
	case 0, 1:
		if targets := r.chooseFriends(u, 1); len(targets) > 0 {
			r.requestFriend(ctx, u, targets[0], true)
		}
	case 2:
		r.declineRequest(ctx, u)
	case 3:
		r.cancelRequest(ctx, u)
	case 4:
		r.listRelationships(ctx, u)
	case 5:
		if u.rng.Float64() < 0.3 {
			r.unfriend(ctx, u)
		} else {
			r.listRelationships(ctx, u)
		}
	}
}

// chooseFriends は u が友達申請を送る相手を n 人まで選ぶ。相手は友達の多い人ほど選ばれ
// やすく（優先的選択）、asia のユーザーは 8 割を asia から、海外のユーザーは 8 割を asia
// から選ぶ（サービスはアジアで広まっていて、海外の人はアジアの友達に誘われて来る）。
func (r *Runner) chooseFriends(u *user, n int) []*user {
	now := r.model.Now()
	var home, asia, others []*user
	byRegion := r.usersByRegion()
	for _, region := range geo.Regions {
		for _, c := range byRegion[region] {
			if c.ID == u.ID || c.stopped.Load() || r.model.Friend(u.ID, c.ID, now) != model.False || r.model.Blocked(u.ID, c.ID, now) != model.False {
				continue
			}
			switch {
			case region == u.Region:
				home = append(home, c)
			case region == geo.Asia:
				asia = append(asia, c)
			default:
				others = append(others, c)
			}
		}
	}
	var targets []*user
	for attempts := 0; len(targets) < n && attempts < 20; attempts++ {
		pool := home
		x := u.rng.Float64()
		switch {
		case u.Region != geo.Asia && x < 0.8 && len(asia) > 0:
			pool = asia
		case u.Region == geo.Asia && x >= 0.8 && len(others) > 0:
			pool = others
		case len(pool) == 0:
			pool = append(asia, others...)
		}
		if len(pool) == 0 {
			break
		}
		target := r.pickTarget(u, pool)
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

// requestFriend は target を検索で探してから友達申請を送る。相手からの申請が保留中なら、
// その申請が承認されて友達になる。
func (r *Runner) requestFriend(ctx context.Context, u *user, target *user, search bool) {
	if search {
		r.searchUser(ctx, u, target)
	}
	w := r.model.BeginFriendship(u.ID, target.ID, true)
	sent := time.Now()
	request, err := u.API.CreateFriendRequest(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, target.ID)
	switch {
	case isStatus(err, http.StatusNotFound):
		// 送っている間に相手が退会すれば 404 が正しい。
		w.Rejected()
		if r.model.Visible(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 有効なユーザー %s への友達申請が 404 になりました", u.Username, target.Username)
		}
	case err == nil && request.Status == "accepted":
		w.Applied()
		r.addFriendship(u.ID, target.ID)
		r.award(u, r.dayNow(u.Region), score.TagFriendRequest)
	case err == nil:
		w.Rejected()
		r.award(u, r.dayNow(u.Region), score.TagFriendRequest)
	case isStatus(err, http.StatusConflict):
		// 送る前に友達になったか、同じ申請が保留中。
		w.Rejected()
	default:
		settle(w, err)
		r.requestError(ctx, u, "%s: 友達申請を送れません: %v", u.Username, err)
	}
}

// searchUser はユーザー名で target を検索し、見つかることを確かめる。
func (r *Runner) searchUser(ctx context.Context, u *user, target *user) {
	sent := time.Now()
	page, err := u.API.SearchUsers(ctx, u.Token, target.Username)
	read := r.model.ReadBetween(sent, time.Now())
	if err != nil {
		r.requestError(ctx, u, "%s: ユーザーを検索できません: %v", u.Username, err)
		return
	}
	found := slices.ContainsFunc(page.Items, func(item client.UserSummary) bool { return item.ID == target.ID })
	if !found && !page.HasMore && r.model.Visible(u.ID, target.ID, read) == model.True {
		r.invalid(ctx, u, "%s: ユーザー名 %s で検索しても見つかりません", u.Username, target.Username)
		return
	}
	for _, item := range page.Items {
		if !strings.Contains(strings.ToLower(item.Username), strings.ToLower(target.Username)) && !strings.Contains(strings.ToLower(item.DisplayName), strings.ToLower(target.Username)) {
			r.invalid(ctx, u, "%s: 検索語 %s を含まないユーザー %s が検索結果にあります", u.Username, target.Username, item.Username)
			return
		}
		if item.ID == u.ID {
			r.invalid(ctx, u, "%s: 自分が検索結果にあります", u.Username)
			return
		}
	}
	r.award(u, r.dayNow(u.Region), score.TagSearch)
}

// declineRequest は届いた申請があれば 1 つ断る。
func (r *Runner) declineRequest(ctx context.Context, u *user) {
	page, err := u.API.Relationships(ctx, u.Token, "incoming-requests", nil)
	if err != nil {
		r.requestError(ctx, u, "%s: 届いた友達申請を取得できません: %v", u.Username, err)
		return
	}
	for _, request := range page.Items {
		if r.model.User(request.User.ID) != nil {
			r.answerRequest(ctx, u, request, "reject")
			return
		}
	}
}

// cancelRequest は送った申請があれば 1 つ取り下げる。
func (r *Runner) cancelRequest(ctx context.Context, u *user) {
	page, err := u.API.Relationships(ctx, u.Token, "outgoing-requests", nil)
	if err != nil {
		r.requestError(ctx, u, "%s: 送った友達申請を取得できません: %v", u.Username, err)
		return
	}
	if len(page.Items) == 0 {
		return
	}
	request := page.Items[len(page.Items)-1]
	_, err = u.API.TransitionFriendRequest(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), u.Token, request.RelationID, "cancel")
	switch {
	case err == nil:
		r.award(u, r.dayNow(u.Region), score.TagFriendDecline)
	case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusNotFound):
		// 相手が先に承認したか断った。
	default:
		r.requestError(ctx, u, "%s: 友達申請を取り下げられません: %v", u.Username, err)
	}
}

// listRelationships はフォロー、フォロワー、友達のどれかの一覧を最後まで読む。フォローの
// 一覧は bench のフォローと突き合わせる。
func (r *Runner) listRelationships(ctx context.Context, u *user) {
	kind := []string{"following", "followers", "friends"}[u.rng.IntN(3)]
	sent := time.Now()
	var ids []string
	var before *client.Relationship
	for {
		page, err := u.API.Relationships(ctx, u.Token, kind, before)
		if err != nil {
			r.requestError(ctx, u, "%s: %s の一覧を取得できません: %v", u.Username, kind, err)
			return
		}
		for _, item := range page.Items {
			ids = append(ids, item.User.ID)
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1]
	}
	read := r.model.ReadBetween(sent, time.Now())
	var problems []string
	switch kind {
	case "following":
		problems = r.model.CheckFollowing(u.ID, ids, read)
	case "friends":
		problems = r.model.CheckFriends(u.ID, ids, read)
	}
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: %s の一覧: %s", u.Username, kind, problem)
	}
	if len(problems) == 0 {
		r.award(u, r.dayNow(u.Region), score.TagRelationships)
	}
}

// unfriend は友達を 1 人解除する。
func (r *Runner) unfriend(ctx context.Context, u *user) {
	now := r.model.Now()
	var friends []*user
	for _, c := range r.usersSnapshot() {
		if c.ID != u.ID && r.model.Friend(u.ID, c.ID, now) == model.True {
			friends = append(friends, c)
		}
	}
	if len(friends) <= 1 {
		return
	}
	target := friends[u.rng.IntN(len(friends))]
	w := r.model.BeginFriendship(u.ID, target.ID, false)
	sent := time.Now()
	err := u.API.Unfriend(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, target.ID)
	settle(w, err)
	switch {
	case err == nil:
		r.award(u, r.dayNow(u.Region), score.TagUnfriend)
	case isStatus(err, http.StatusConflict):
	case isStatus(err, http.StatusNotFound):
		// 送っている間に相手が退会すれば 404 が正しい。
		if r.model.Visible(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 有効なユーザー %s との友達の解除が 404 になりました", u.Username, target.Username)
		}
	default:
		r.requestError(ctx, u, "%s: 友達を解除できません: %v", u.Username, err)
	}
}

// groom は自分のアカウントを 1 つ整える。プロフィールの変更、プロフィール画像の変更と
// 削除、認定公開アカウントのフォローと解除、今日の投稿のキャプションの変更、ブロックと解除。
// 初期データのユーザーはプロフィールを変えない（履歴の検証で投稿者のプロフィールを比べ、
// initialize で戻せない画像もある）。
func (r *Runner) groom(ctx context.Context, u *user, s *session) {
	switch u.rng.IntN(6) {
	case 0:
		switch {
		case u.registered && u.rng.Float64() < 0.3:
			r.rename(ctx, u)
		case u.registered:
			r.updateBio(ctx, u)
		default:
			r.toggleFollow(ctx, u)
		}
	case 1:
		if u.registered {
			r.changeProfileImage(ctx, u)
		} else {
			r.editCaption(ctx, u, s)
		}
	case 2:
		r.toggleFollow(ctx, u)
	case 3:
		r.editCaption(ctx, u, s)
	case 4:
		r.toggleFollow(ctx, u)
	case 5:
		r.toggleBlock(ctx, u)
	}
}

// updateBio は自己紹介を変える。
// rename は表示名を変える。
func (r *Runner) rename(ctx context.Context, u *user) {
	name := dataset.DisplayName(u.rng)
	profile, err := u.API.UpdateMe(ctx, u.Token, &name, nil, nil)
	switch {
	case err != nil:
		r.requestError(ctx, u, "%s: 表示名を変えられません: %v", u.Username, err)
	case profile.ID != u.ID || profile.DisplayName != name:
		r.invalid(ctx, u, "%s: 表示名を変えた応答が違います", u.Username)
	default:
		r.award(u, r.dayNow(u.Region), score.TagProfileUpdate)
	}
}

func (r *Runner) updateBio(ctx context.Context, u *user) {
	bio := dataset.Bio(u.rng)
	profile, err := u.API.UpdateMe(ctx, u.Token, nil, &bio, nil)
	switch {
	case err != nil:
		r.requestError(ctx, u, "%s: プロフィールを変えられません: %v", u.Username, err)
	case profile.ID != u.ID || profile.Bio != bio:
		r.invalid(ctx, u, "%s: プロフィールを変えた応答が違います", u.Username)
	default:
		r.award(u, r.dayNow(u.Region), score.TagProfileUpdate)
	}
}

// changeProfileImage はプロフィール画像を変えるか消し、自分のプロフィールで確かめる。
func (r *Runner) changeProfileImage(ctx context.Context, u *user) {
	if u.profileImage != nil && u.rng.IntN(3) == 0 {
		if err := u.API.DeleteProfileImage(ctx, u.Token); err != nil {
			r.requestError(ctx, u, "%s: プロフィール画像を消せません: %v", u.Username, err)
			return
		}
		u.profileImage = nil
	} else {
		image := r.photos.Profile(hashOf(r.seed, "profile", u.Username, itoa(u.rng.IntN(1<<30))))
		if err := u.API.PutProfileImage(ctx, u.Token, image); err != nil {
			r.requestError(ctx, u, "%s: プロフィール画像を変えられません: %v", u.Username, err)
			return
		}
		sum := sha256.Sum256(image)
		u.profileImage = &sum
	}
	profile, err := u.API.Me(ctx, u.Token)
	if err != nil {
		r.requestError(ctx, u, "%s: プロフィールを取得できません: %v", u.Username, err)
		return
	}
	switch {
	case u.profileImage == nil && profile.ProfileImageURL != nil:
		r.invalid(ctx, u, "%s: 消したプロフィール画像が残っています", u.Username)
		return
	case u.profileImage != nil && profile.ProfileImageURL == nil:
		r.invalid(ctx, u, "%s: プロフィール画像がありません", u.Username)
		return
	case u.profileImage != nil:
		data, err := u.API.Image(ctx, *profile.ProfileImageURL)
		if err != nil {
			r.requestError(ctx, u, "%s: プロフィール画像を取得できません: %v", u.Username, err)
			return
		}
		if sha256.Sum256(data) != *u.profileImage {
			r.invalid(ctx, u, "%s: プロフィール画像が送った画像と違います", u.Username)
			return
		}
	}
	r.award(u, r.dayNow(u.Region), score.TagProfileImage)
}

// toggleFollow は認定公開アカウントを 1 つフォローするか、フォローしているものを 1 つ解除する。
func (r *Runner) toggleFollow(ctx context.Context, u *user) {
	if len(u.follows) > 0 && u.rng.IntN(3) == 0 {
		var followees []string
		for id := range u.follows {
			followees = append(followees, id)
		}
		slices.Sort(followees)
		id := followees[u.rng.IntN(len(followees))]
		w := r.model.BeginFollow(u.ID, id, false)
		err := u.API.Unfollow(client.MayFailWith(ctx, http.StatusConflict), u.Token, id)
		settle(w, err)
		switch {
		case err == nil:
			delete(u.follows, id)
			r.award(u, r.dayNow(u.Region), score.TagUnfollow)
		case isStatus(err, http.StatusConflict):
			delete(u.follows, id)
		default:
			r.requestError(ctx, u, "%s: フォローを解除できません: %v", u.Username, err)
		}
		return
	}
	r.followCertified(ctx, u, 1)
}

// followCertified は認定公開アカウントを n 件までフォローする。最近認定された人ほど
// 選ばれやすい（話題の人をフォローする）。
func (r *Runner) followCertified(ctx context.Context, u *user, n int) {
	certified := r.certifiedSnapshot()
	now := r.model.Now()
	for range n {
		var candidates []string
		for _, id := range certified {
			if id != u.ID && !u.follows[id] && r.model.Blocked(u.ID, id, now) == model.False {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return
		}
		// 後ろ（最近認定された人）ほど重くする。
		id := candidates[len(candidates)-1-min(len(candidates)-1, int(float64(len(candidates))*u.rng.Float64()*u.rng.Float64()))]
		w := r.model.BeginFollow(u.ID, id, true)
		sent := time.Now()
		err := u.API.Follow(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, id)
		settle(w, err)
		switch {
		case err == nil:
			u.follows[id] = true
			r.award(u, r.dayNow(u.Region), score.TagFollow)
		case isStatus(err, http.StatusConflict):
			u.follows[id] = true
		case isStatus(err, http.StatusNotFound):
			// 送っている間に相手が退会すれば 404 が正しい。
			if r.model.Visible(u.ID, id, r.model.ReadBetween(sent, time.Now())) == model.True {
				r.invalid(ctx, u, "%s: 有効な認定公開アカウント %s のフォローが 404 になりました", u.Username, id)
			}
			return
		default:
			r.requestError(ctx, u, "%s: 認定公開アカウントをフォローできません: %v", u.Username, err)
			return
		}
	}
}

// editCaption は今日の投稿のキャプションを変える。投稿期間が閉じていれば 403 が正しい。
func (r *Runner) editCaption(ctx context.Context, u *user, s *session) {
	if s.postID == "" {
		return
	}
	caption := dataset.CommentBody(u.rng) + "（編集）"
	w := r.model.BeginUpdateCaption(s.postID, caption)
	sent := time.Now()
	post, err := u.API.UpdatePostCaption(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), u.Token, s.postID, caption)
	settle(w, err)
	read := r.model.ReadBetween(sent, time.Now())
	switch {
	case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusNotFound):
		if r.model.WindowOpen(s.postID, read) == model.True && r.model.PostLive(s.postID, read) == model.True {
			r.invalid(ctx, u, "%s: 今日の投稿 %s のキャプションを変えられません", u.Username, s.postID)
		}
	case err != nil:
		r.requestError(ctx, u, "%s: キャプションを変えられません: %v", u.Username, err)
	case post.Caption != caption:
		r.invalid(ctx, u, "%s: キャプションを変えた応答が違います", u.Username)
	default:
		r.award(u, s.day, score.TagCaption)
	}
}

// toggleBlock は走行中にブロックした相手がいれば解除し、いなければ友達でない同じ地域の
// ユーザーを 1 人ブロックする。
func (r *Runner) toggleBlock(ctx context.Context, u *user) {
	if len(u.blocking) > 0 {
		id := u.blocking[len(u.blocking)-1]
		w := r.model.BeginUnblock(u.ID, id)
		err := u.API.Unblock(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, id)
		settle(w, err)
		switch {
		case err == nil:
			u.blocking = u.blocking[:len(u.blocking)-1]
			r.award(u, r.dayNow(u.Region), score.TagUnblock)
		case isStatus(err, http.StatusConflict) || isStatus(err, http.StatusNotFound):
			u.blocking = u.blocking[:len(u.blocking)-1]
		default:
			r.requestError(ctx, u, "%s: ブロックを解除できません: %v", u.Username, err)
		}
		return
	}
	now := r.model.Now()
	var candidates []*user
	// 同じリージョンの人から選ぶ。ほかのユーザーのリージョンは移動と競合しないよう、ロックの
	// 中で取った一覧で見る。
	for _, c := range r.usersByRegion()[u.Region] {
		// 退会した人は選ばない（Visible は相手が有効で、どちら向きにもブロックしていないこと）。
		if c.ID != u.ID && !c.certified.Load() && r.model.Friend(u.ID, c.ID, now) == model.False && r.model.Visible(u.ID, c.ID, now) == model.True {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return
	}
	target := candidates[u.rng.IntN(len(candidates))]
	w := r.model.BeginBlock(u.ID, target.ID)
	sent := time.Now()
	err := u.API.Block(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, target.ID)
	settle(w, err)
	switch {
	case err == nil:
		u.blocking = append(u.blocking, target.ID)
		r.award(u, r.dayNow(u.Region), score.TagBlock)
	case isStatus(err, http.StatusConflict):
	case isStatus(err, http.StatusNotFound):
		// 送っている間に相手が退会すれば 404 が正しい。
		if r.model.Visible(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 有効なユーザー %s のブロックが 404 になりました", u.Username, target.Username)
		}
	default:
		r.requestError(ctx, u, "%s: ブロックできません: %v", u.Username, err)
	}
}
