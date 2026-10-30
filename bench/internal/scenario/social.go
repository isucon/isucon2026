package scenario

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

// reachOutOnRegister と reachOutPerDay は、登録したときと毎日の投稿のあとで送る友達申請の
// 数の上限。顔の広い人も一度に送りすぎず、最初の投稿に間に合うようにする。
// discoveriesPerDay は、フィードで見た人に送る友達申請の 1 日の上限。
const (
	reachOutOnRegister = 5
	reachOutPerDay     = 3
	discoveriesPerDay  = 2
)

// reachOut は、登録したあとでまだ送っていない友達申請を n 件まで送る。送れた分だけ減らし、
// 申請する相手が足りなかった分や送れなかった分は、あとの日に送る。
func (r *Runner) reachOut(ctx context.Context, u *user, n int) {
	n = min(n, u.circleLeft)
	if n <= 0 || u.stopped.Load() {
		return
	}
	for _, target := range r.chooseFriends(u, n) {
		if r.requestFriend(ctx, u, target, u.rng.IntN(2) == 0) {
			u.circleLeft--
		}
	}
}

// discover は、フィードで見た投稿の投稿者に友達申請を送る（知り合いかもしれない人）。
// 投稿者が bench の知っている、止まっていないユーザーで、まだ友達でもブロックの関係でも
// ないときだけ送る。
func (r *Runner) discover(ctx context.Context, u *user, s *session, authorID string) {
	r.mu.Lock()
	target := r.byID[authorID]
	r.mu.Unlock()
	if target == nil || target == u || target.stopped.Load() {
		return
	}
	now := r.model.Now()
	if r.model.Friend(u.ID, target.ID, now) != model.False || r.model.Blocked(u.ID, target.ID, now) != model.False {
		return
	}
	s.discovered++
	r.mu.Lock()
	r.discoveries[u.Region]++
	r.mu.Unlock()
	r.requestFriend(ctx, u, target, false)
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
	for attempts := 0; len(targets) < n && attempts < 4*n+10; attempts++ {
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
// その申請が承認されて友達になる。申請を送れたら（友達になれたら）true を返す。
func (r *Runner) requestFriend(ctx context.Context, u *user, target *user, search bool) bool {
	if search {
		r.searchUser(ctx, u, target)
	}
	w := r.model.BeginFriendship(u.ID, target.ID, true)
	creation := r.model.BeginFriendRequest(u.ID, target.ID)
	sent := time.Now()
	request, err := u.API.CreateFriendRequest(client.MayFailWith(ctx, http.StatusConflict, http.StatusNotFound), u.Token, target.ID)
	// 保留中になった申請だけを記録する（相手の申請で友達になれば、この申請は残らない）。
	if err == nil && request.Status != "accepted" {
		r.model.RequestedFriend(creation, request.RelationID)
	} else {
		creation.Rejected()
	}
	switch {
	case isStatus(err, http.StatusNotFound):
		// 送っている間に相手が退会すれば 404 が正しい。
		settle(w, err)
		if r.model.Visible(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 有効なユーザー %s への友達申請が 404 になりました", u.Username, target.Username)
		}
	case err == nil && request.RelationID == "" || err == nil && request.Status != "pending" && request.Status != "accepted":
		// 申請ができたのか、友達になったのかわからない。
		w.Unknown()
		r.invalid(ctx, u, "%s: %s への友達申請の応答が不正です（relationId %q、status %q）", u.Username, target.Username, request.RelationID, request.Status)
	case err == nil && request.Status == "accepted":
		w.Applied()
		r.addFriendship(u.ID, target.ID)
		r.award(u, r.dayNow(u.Region), score.TagFriendRequest)
		return true
	case err == nil:
		w.Rejected()
		r.award(u, r.dayNow(u.Region), score.TagFriendRequest)
		return true
	case isStatus(err, http.StatusConflict):
		// 送る前に友達になったか、同じ申請が保留中。
		settle(w, err)
	default:
		settle(w, err)
		r.requestError(ctx, u, "%s: 友達申請を送れません: %v", u.Username, err)
	}
	return false
}

// searchPages は、検索で target が見つからないときに読むページ数の上限。ユーザー名は
// 初期データと同じ形でほぼ重ならないので、ふつうは 1 ページ目で見つかる。
const searchPages = 5

// searchUser はユーザー名で target を検索し、見つかることを確かめる。見つかるまで続きの
// ページを読む。読んだ結果に、ブロックの関係にある人や退会した人がいてはならない。
func (r *Runner) searchUser(ctx context.Context, u *user, target *user) {
	sent := time.Now()
	var items []client.UserSummary
	var after *client.UserSummary
	found := false
	for pages := 1; ; pages++ {
		page, err := u.API.SearchUsers(ctx, u.Token, target.Username, after)
		if err != nil {
			r.requestError(ctx, u, "%s: ユーザーを検索できません: %v", u.Username, err)
			return
		}
		items = append(items, page.Items...)
		found = slices.ContainsFunc(page.Items, func(item client.UserSummary) bool { return item.ID == target.ID })
		if found || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			r.invalid(ctx, u, "%s: ユーザー名 %s の検索結果が、続きがあるのに空です", u.Username, target.Username)
			return
		}
		if pages == searchPages {
			// 見つかるかどうか確かめきれないので加点しない。
			return
		}
		after = &page.Items[len(page.Items)-1]
	}
	read := r.model.ReadBetween(sent, time.Now())
	if !found && r.model.Visible(u.ID, target.ID, read) == model.True {
		r.invalid(ctx, u, "%s: ユーザー名 %s で検索しても見つかりません", u.Username, target.Username)
		return
	}
	for _, item := range items {
		// bench の知らない人（不正な利用者や、登録の結果がわからなかった人）は確かめられない。
		if r.model.User(item.ID) != nil && r.model.Visible(u.ID, item.ID, read) == model.False {
			r.invalid(ctx, u, "%s: ブロックの関係にあるか退会したユーザー %s が検索結果にあります", u.Username, item.Username)
			return
		}
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
	closing := r.model.BeginCloseFriendRequest(request.RelationID)
	result, err := u.API.TransitionFriendRequest(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), u.Token, request.RelationID, "cancel")
	malformed := err == nil && (result.RelationID != request.RelationID || result.Status != "cancelled")
	if malformed {
		// 取り下げられたのかわからない。
		closing.Unknown()
	} else {
		settleRetried(closing, err)
	}
	switch {
	case malformed:
		r.invalid(ctx, u, "%s: 友達申請 %s を取り下げた応答が不正です（relationId %q、status %q）", u.Username, request.RelationID, result.RelationID, result.Status)
	case err == nil:
		r.award(u, r.dayNow(u.Region), score.TagFriendDecline)
	case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusNotFound):
		// 相手が先に承認したか断った。
	default:
		r.requestError(ctx, u, "%s: 友達申請を取り下げられません: %v", u.Username, err)
	}
}

// listRelationships はフォロー、フォロワー、友達のどれかの一覧を最後まで読み、bench の
// フォローや友達関係と突き合わせる。
func (r *Runner) listRelationships(ctx context.Context, u *user) {
	r.readRelationships(ctx, u, []string{"following", "followers", "friends"}[u.rng.IntN(3)])
}

// readRelationships は kind の関係の一覧を最後まで読み、bench のフォローや友達関係と突き合わせる。
func (r *Runner) readRelationships(ctx context.Context, u *user, kind string) {
	sent := time.Now()
	var items []client.Relationship
	var before *client.Relationship
	for {
		page, err := u.API.Relationships(ctx, u.Token, kind, before)
		if err != nil {
			r.requestError(ctx, u, "%s: %s の一覧を取得できません: %v", u.Username, kind, err)
			return
		}
		items = append(items, page.Items...)
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1]
	}
	read := r.model.ReadBetween(sent, time.Now())
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.User.ID
	}
	var problems []string
	if problem := relationProblem(kind, items); problem != "" {
		problems = append(problems, problem)
	}
	switch kind {
	case "following":
		problems = append(problems, r.model.CheckFollowing(u.ID, ids, read)...)
	case "followers":
		problems = append(problems, r.model.CheckFollowers(u.ID, ids, read)...)
	case "friends":
		problems = append(problems, r.model.CheckFriends(u.ID, ids, read)...)
	}
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: %s の一覧: %s", u.Username, kind, problem)
	}
	if len(problems) == 0 {
		r.award(u, r.dayNow(u.Region), score.TagRelationships)
	}
}

// relationProblem は、関係の一覧 items の各項目が kind の関係を表し（status が kind）、
// 関係の ID と更新日時を持つことを確かめる。違う項目があれば最初の 1 つを説明して返す。
func relationProblem(kind string, items []client.Relationship) string {
	for _, item := range items {
		if item.Status != kind || item.RelationID == "" || item.UpdatedAt.IsZero() {
			return fmt.Sprintf("ユーザー %s の項目が不正です（relationId %q、status %q、updatedAt %s）", item.User.ID, item.RelationID, item.Status, item.UpdatedAt.Format(time.RFC3339Nano))
		}
	}
	return ""
}

// settleRelation は関係の書き込みの結果を settle と同じくモデルに記録する。ただし 503 を
// 受けて送り直したあとの 4xx（もう友達でない、もうフォローしているなどの 409）は、前の
// 試行で反映されたかもしれないので結果不明とする。
func settleRelation(w *model.Write, err error) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Attempts > 1 && isRejection(err) {
		w.Unknown()
		return
	}
	settle(w, err)
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
	settleRelation(w, err)
	switch {
	case err == nil:
		r.removeFriendship(u.ID, target.ID)
		r.award(u, r.dayNow(u.Region), score.TagUnfriend)
	case isStatus(err, http.StatusConflict):
		// 送っている間に相手が解除するかブロックしていれば 409 が正しい。
		if r.model.Friend(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 友達の %s との友達の解除が 409 になりました", u.Username, target.Username)
		}
	case isStatus(err, http.StatusNotFound):
		// 送っている間に相手が退会すれば 404 が正しい。
		if r.model.Visible(u.ID, target.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 有効なユーザー %s との友達の解除が 404 になりました", u.Username, target.Username)
		}
	default:
		r.requestError(ctx, u, "%s: 友達を解除できません: %v", u.Username, err)
	}
}

// removeFriendship は a と b が友達でなくなったことを、友達申請の相手を選ぶ重み
// （r.friendCount）に反映する。
func (r *Runner) removeFriendship(a, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range []string{a, b} {
		r.friendCount[id] = max(0, r.friendCount[id]-1)
	}
}

// countFriends は初期データの友達関係を持つ u の、今の友達（退会した人を除く）の数を
// r.friendCount に入れる。
func (r *Runner) countFriends(u *user) {
	now := r.model.Now()
	n := 0
	for _, id := range r.model.FriendsOf(u.ID, now) {
		if r.model.Visible(u.ID, id, now) == model.True {
			n++
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.friendCount[u.ID] += n
}

// dropFriendsOf は退会した u の友達それぞれの友達の数を 1 ずつ減らす。
func (r *Runner) dropFriendsOf(u *user) {
	for _, id := range r.model.FriendsOf(u.ID, r.model.Now()) {
		r.removeFriendship(u.ID, id)
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

// rename は表示名を変える。変えた値は走行後の整合性チェックで確かめる。
func (r *Runner) rename(ctx context.Context, u *user) {
	name := dataset.DisplayName(u.rng)
	profile, err := u.API.UpdateMe(ctx, u.Token, &name, nil, nil)
	u.displayNames = changedProfile(u.displayNames, name, err == nil && profile.ID == u.ID && profile.DisplayName == name, err)
	switch {
	case err != nil:
		r.requestError(ctx, u, "%s: 表示名を変えられません: %v", u.Username, err)
	case profile.ID != u.ID || profile.DisplayName != name:
		r.invalid(ctx, u, "%s: 表示名を変えた応答が違います", u.Username)
	default:
		r.award(u, r.dayNow(u.Region), score.TagProfileUpdate)
	}
}

// updateBio は自己紹介を変える。変えた値は走行後の整合性チェックで確かめる。
func (r *Runner) updateBio(ctx context.Context, u *user) {
	bio := dataset.Bio(u.rng)
	profile, err := u.API.UpdateMe(ctx, u.Token, nil, &bio, nil)
	u.bios = changedProfile(u.bios, bio, err == nil && profile.ID == u.ID && profile.Bio == bio, err)
	switch {
	case err != nil:
		r.requestError(ctx, u, "%s: プロフィールを変えられません: %v", u.Username, err)
	case profile.ID != u.ID || profile.Bio != bio:
		r.invalid(ctx, u, "%s: プロフィールを変えた応答が違います", u.Username)
	default:
		r.award(u, r.dayNow(u.Region), score.TagProfileUpdate)
	}
}

// changedProfile は、表示名か自己紹介としてありうる値 values に、value への変更の結果を
// 反映する。受け付けられたら value だけになり、拒否されたら変わらず、結果がわからなければ
// （応答が違う、5xx やタイムアウト、503 を受けて送り直したあとの 4xx）value も加わる。
// values が nil なら今の値を知らないので、受け付けられるまで nil のまま（確かめない）。
func changedProfile(values []string, value string, accepted bool, err error) []string {
	var apiErr *client.APIError
	switch {
	case accepted:
		return []string{value}
	case values == nil || slices.Contains(values, value):
		return values
	case isRejection(err) && !(errors.As(err, &apiErr) && apiErr.Attempts > 1):
		return values
	default:
		return append(values, value)
	}
}

// changeProfileImage はプロフィール画像を変えるか消し、自分のプロフィールで確かめる。
func (r *Runner) changeProfileImage(ctx context.Context, u *user) {
	var image []byte
	if u.profileImage != nil && u.rng.IntN(3) == 0 {
		if err := u.API.DeleteProfileImage(ctx, u.Token); err != nil {
			r.requestError(ctx, u, "%s: プロフィール画像を消せません: %v", u.Username, err)
			return
		}
		u.profileImage = nil
	} else {
		image = r.photos.Profile(hashOf(r.seed, "profile", u.Username, itoa(u.rng.IntN(1<<30))))
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
		if !client.SameImage(data, image) {
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
		sent := time.Now()
		err := u.API.Unfollow(client.MayFailWith(ctx, http.StatusConflict), u.Token, id)
		settleRelation(w, err)
		switch {
		case err == nil:
			delete(u.follows, id)
			r.award(u, r.dayNow(u.Region), score.TagUnfollow)
		case isStatus(err, http.StatusConflict):
			// 送っている間に相手がブロックしてフォローが消えていれば 409 が正しい。確実に
			// フォローしているなら、あとでもう一度解除する。
			if r.model.Following(u.ID, id, r.model.ReadBetween(sent, time.Now())) == model.True {
				r.invalid(ctx, u, "%s: フォローしている %s のフォローの解除が 409 になりました", u.Username, id)
				return
			}
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
		settleRelation(w, err)
		switch {
		case err == nil:
			u.follows[id] = true
			r.award(u, r.dayNow(u.Region), score.TagFollow)
		case isStatus(err, http.StatusConflict):
			// もうフォローしているか、送っている間にどちらかがブロックしていれば 409 が正しい。
			// フォローしていないことが確かなら、解除の候補には入れない。
			read := r.model.ReadBetween(sent, time.Now())
			switch {
			case r.model.Following(u.ID, id, read) != model.False:
				u.follows[id] = true
			case r.model.Blocked(u.ID, id, read) == model.False:
				r.invalid(ctx, u, "%s: フォローしていない認定公開アカウント %s のフォローが 409 になりました", u.Username, id)
				return
			}
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
		sent := time.Now()
		// ブロックしていなければアプリは 403 を返す（409 を返す実装もありうる）。
		err := u.API.Unblock(client.MayFailWith(ctx, http.StatusForbidden, http.StatusConflict, http.StatusNotFound), u.Token, id)
		settleRelation(w, err)
		switch {
		case err == nil:
			u.blocking = u.blocking[:len(u.blocking)-1]
			r.award(u, r.dayNow(u.Region), score.TagUnblock)
		case r.bannedErr(u, err):
			// BAN による 403 は解除の拒否ではない。u はもう動かない。
		case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusConflict):
			// 確実にブロックしているなら、あとでもう一度解除する。
			if r.model.Blocking(u.ID, id, r.model.ReadBetween(sent, time.Now())) == model.True {
				r.invalid(ctx, u, "%s: ブロックしている %s のブロックの解除が拒否されました", u.Username, id)
				return
			}
			u.blocking = u.blocking[:len(u.blocking)-1]
		case isStatus(err, http.StatusNotFound):
			// 相手が退会していれば 404 が正しい。
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

// FriendStats は、あるリージョンの止まっていない負荷ユーザーの友達の数（Friends）と、友達の
// 友達のうち止まっていない負荷ユーザーの数（FoF）。友達の友達フィードの候補になりうる人の
// 数の目安。
type FriendStats struct {
	Users      int     `json:"users"`
	FriendsAvg float64 `json:"friendsAvg"`
	FriendsP50 int     `json:"friendsP50"`
	FriendsP90 int     `json:"friendsP90"`
	FriendsMax int     `json:"friendsMax"`
	FoFAvg     float64 `json:"fofAvg"`
	FoFP90     int     `json:"fofP90"`
	FoFMax     int     `json:"fofMax"`
	// Discoveries はフィードで見た人に送った友達申請の数。
	Discoveries int `json:"discoveries"`
}

// friendStats は走行の終わりの友達の数を、リージョンごとに数える。
func (r *Runner) friendStats() map[string]FriendStats {
	now := r.model.Now()
	byRegion := r.usersByRegion()
	active := map[string]bool{}
	for _, list := range byRegion {
		for _, u := range list {
			if !u.stopped.Load() {
				active[u.ID] = true
			}
		}
	}
	cache := map[string][]string{}
	friendsOf := func(id string) []string {
		if friends, ok := cache[id]; ok {
			return friends
		}
		friends := r.model.FriendsOf(id, now)
		cache[id] = friends
		return friends
	}
	out := map[string]FriendStats{}
	for region, list := range byRegion {
		var friends, fofs []int
		for _, u := range list {
			if !active[u.ID] {
				continue
			}
			direct := friendsOf(u.ID)
			seen := map[string]bool{u.ID: true}
			for _, f := range direct {
				seen[f] = true
			}
			fof := 0
			for _, f := range direct {
				for _, g := range friendsOf(f) {
					if !seen[g] {
						seen[g] = true
						if active[g] {
							fof++
						}
					}
				}
			}
			friends = append(friends, len(direct))
			fofs = append(fofs, fof)
		}
		if len(friends) == 0 {
			continue
		}
		slices.Sort(friends)
		slices.Sort(fofs)
		stats := FriendStats{
			Users:      len(friends),
			FriendsAvg: float64(sum(friends)) / float64(len(friends)),
			FriendsP50: percentileOf(friends, 0.5),
			FriendsP90: percentileOf(friends, 0.9),
			FriendsMax: friends[len(friends)-1],
			FoFAvg:     float64(sum(fofs)) / float64(len(fofs)),
			FoFP90:     percentileOf(fofs, 0.9),
			FoFMax:     fofs[len(fofs)-1],
		}
		r.mu.Lock()
		stats.Discoveries = r.discoveries[region]
		r.mu.Unlock()
		out[region] = stats
	}
	return out
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
