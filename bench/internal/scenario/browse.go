package scenario

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// feedWeights はフィードを選ぶ重みの全体の平均。人ごとの重みは persona で揺らす。
var feedWeights = []struct {
	feed   string
	weight float64
}{{"friends", 0.35}, {"friends-of-friends", 0.25}, {"public", 0.25}, {"following", 0.15}}

// deletedImageGrace は、投稿を削除してからその画像を返さなくなるまでに許す時間。
const deletedImageGrace = 5 * time.Second

// browse は 1 回分の閲覧。ときどき友達申請を見たり、関係やプロフィールを整えたりする。
// フィードを 1 つ選んで読み、見えた投稿を 1 つ開いて、過ごし方に合わせて画像、詳細、
// 投稿者のプロフィール、RealMoji やコメントを見たり送ったりする。
func (r *Runner) browse(ctx context.Context, u *user, s *session) {
	social, groom := 0.03*u.persona.sociability, 0.02*u.persona.grooming
	switch x := u.rng.Float64(); {
	case x < 0.08:
		r.acceptRequests(ctx, u)
	case x < 0.08+social:
		r.social(ctx, u, s)
	case x < 0.08+social+groom:
		r.groom(ctx, u, s)
	case x < 0.10+social+groom && u.cohort:
		// 初期データのユーザーは、ときどき自分の過去の投稿を見返す。
		r.readHistory(ctx, u)
	}
	feed := u.pickFeed()
	page, ok := r.readFeed(ctx, u, feed, nil, r.sample())
	// 続きがあれば、ときどき次のページも読む（スクロール）。
	for ok && page.HasMore && len(page.Items) > 0 && u.rng.Float64() < u.persona.scroll {
		last := page.Items[len(page.Items)-1]
		page, ok = r.readFeed(ctx, u, feed, &last, r.sample())
	}
	if !ok {
		return
	}
	if s.feeds < scoredFeedsPerDay {
		s.feeds++
		r.award(u, s.day, score.TagFeed)
	}
	var candidates []client.FeedPost
	for _, post := range page.Items {
		if r.model.Post(post.ID) != nil {
			candidates = append(candidates, post)
		}
	}
	if len(candidates) == 0 {
		return
	}
	post := r.pickPost(u, candidates)
	// 友達の友達フィード（まれに公開フィード）で見た人に、ときどき友達申請を送る。
	discover := 0.0
	switch feed {
	case "friends-of-friends":
		discover = u.persona.discover
	case "public":
		discover = u.persona.discover / 3
	}
	if s.discovered < discoveriesPerDay && u.rng.Float64() < discover {
		r.discover(ctx, u, s, post.Author.ID)
	}
	if u.rng.Float64() < u.persona.detail {
		r.openPost(ctx, u, s, post)
	}
	if s.images < scoredImagesPerDay && u.rng.Float64() < u.persona.image && r.checkImage(ctx, u, post) {
		s.images++
		r.award(u, s.day, score.TagImage)
	}
	if u.rng.Float64() < u.persona.profile {
		r.viewProfile(ctx, u, s, post.Author.ID)
	}
	switch u.activity {
	case reactor:
		if s.realMojis < realMojisPerDay && r.react(ctx, u, s, post) {
			s.realMojis++
		}
		if u.rng.Float64() < 0.3 {
			r.listRealMojis(ctx, u, s, post.ID)
		}
		if len(s.myReactions) > 0 && u.rng.Float64() < 0.1 {
			r.deleteRealMoji(ctx, u, s)
		}
	case commenter:
		if s.comments < commentsPerDay && r.comment(ctx, u, s, post) {
			s.comments++
		}
		if u.rng.Float64() < 0.3 {
			r.listComments(ctx, u, s, post.ID)
		}
		if len(s.myComments) > 0 && u.rng.Float64() < 0.1 {
			r.deleteComment(ctx, u, s)
		}
	default:
		if u.rng.Float64() < 0.15 {
			r.listRealMojis(ctx, u, s, post.ID)
		}
		if u.rng.Float64() < 0.15 {
			r.listComments(ctx, u, s, post.ID)
		}
	}
}

// openPost は投稿を 1 件開き、見えるべきかと内容を確かめる。
func (r *Runner) openPost(ctx context.Context, u *user, s *session, post client.FeedPost) {
	sent := time.Now()
	got, err := u.API.GetPost(client.MayFailWith(ctx, http.StatusNotFound), u.Token, post.ID)
	read := r.model.ReadBetween(sent, time.Now())
	var violation string
	var problems []string
	switch {
	case isStatus(err, http.StatusNotFound):
		violation, problems = r.model.CheckPost(u.ID, post.ID, nil, read)
	case err != nil:
		r.requestError(ctx, u, "%s: 投稿 %s を取得できません: %v", u.Username, post.ID, err)
		return
	default:
		violation, problems = r.model.CheckPost(u.ID, post.ID, &got, read)
	}
	if violation != "" {
		r.board.Criticalf("%s: %s", u.Username, violation)
		return
	}
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: %s", u.Username, problem)
	}
	if err == nil && len(problems) == 0 && s.details < scoredDetailsPerDay {
		s.details++
		r.award(u, s.day, score.TagPostDetail)
	}
}

// viewProfile は投稿者のプロフィールを見る。
func (r *Runner) viewProfile(ctx context.Context, u *user, s *session, userID string) {
	sent := time.Now()
	profile, err := u.API.GetUser(client.MayFailWith(ctx, http.StatusNotFound), u.Token, userID)
	read := r.model.ReadBetween(sent, time.Now())
	known := r.model.User(userID)
	switch {
	case isStatus(err, http.StatusNotFound):
		if r.model.Visible(u.ID, userID, read) == model.True {
			r.invalid(ctx, u, "%s: ユーザー %s のプロフィールが 404 になりました", u.Username, userID)
		}
	case err != nil:
		r.requestError(ctx, u, "%s: ユーザー %s のプロフィールを取得できません: %v", u.Username, userID, err)
	case known != nil && (profile.ID != userID || profile.Username != known.Username):
		r.invalid(ctx, u, "%s: ユーザー %s のプロフィールが違います", u.Username, userID)
	case known != nil && r.model.Visible(u.ID, userID, read) == model.False:
		// ブロックの関係にある人や退会した人のプロフィールは見えてはいけない。
		r.invalid(ctx, u, "%s: 見えないはずのユーザー %s のプロフィールが返りました", u.Username, userID)
	default:
		if s.profiles < scoredProfilesPerDy {
			s.profiles++
			r.award(u, s.day, score.TagProfileView)
		}
	}
}

// react は RealMoji を送る。送ってよいとモデルが判定した投稿にだけ送る。送っている間に
// 投稿が見えなくなることもあるので、結果の正否は送った区間でのモデルの判定と比べる。
// 見えるはずなのに拒否されたらエラー、見えないはずなのに受け付けられたら違反。送れたら true。
func (r *Runner) react(ctx context.Context, u *user, s *session, post client.FeedPost) bool {
	sent := time.Now()
	if post.Author.ID == u.ID || r.model.CanView(u.ID, post.ID, r.model.ReadBetween(sent, sent)) != model.True {
		return false
	}
	kind := u.reactionType()
	w := r.model.BeginReplaceRealMoji(post.ID, u.ID)
	reaction, err := u.API.ReplaceRealMoji(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), u.Token, post.ID, kind, r.photos.RealMoji(hashOf(r.seed, "realmoji", u.Username, itoa(s.day), itoa(s.realMojis))))
	allowed := r.model.CanView(u.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case r.bannedErr(u, err):
		// BAN は閲覧権限の拒否より先に判定する（拒否されたので反映されていない）。
		w.Rejected()
	case isStatus(err, http.StatusNotFound) && allowed != model.True:
		// 送っている間に投稿者が退会していれば 404 が正しい。
		w.Rejected()
	case isStatus(err, http.StatusNotFound):
		w.Rejected()
		r.invalid(ctx, u, "%s: 見える投稿 %s への RealMoji が 404 になりました", u.Username, post.ID)
	case err == nil && allowed == model.False:
		w.Unknown()
		r.board.Criticalf("%s: 見えない投稿 %s に RealMoji を送れてしまいました", u.Username, post.ID)
	case err == nil:
		// 応答の誤った RealMoji は、反映されたかどうかもわからないものとして扱う（空の ID を
		// 「RealMoji がない」と記録しない）。
		if problem, weight := r.checkReaction(u, kind, reaction); problem != "" {
			w.Unknown()
			r.invalidN(ctx, u, weight, "%s: 投稿 %s への RealMoji の応答が不正です: %s", u.Username, post.ID, problem)
			return false
		}
		if !r.model.ReplacedRealMoji(w, reaction) {
			w.Unknown()
			r.invalid(ctx, u, "%s: 投稿 %s への RealMoji の応答の ID %s が、ほかの RealMoji と同じです", u.Username, post.ID, reaction.ID)
			return false
		}
		r.award(u, s.day, score.TagRealMoji)
		s.myReactions = append(s.myReactions, post.ID)
		return true
	case isStatus(err, http.StatusForbidden) && allowed != model.True:
		w.Rejected()
	case isStatus(err, http.StatusForbidden):
		w.Rejected()
		r.invalid(ctx, u, "%s: 見える投稿 %s への RealMoji が拒否されました", u.Username, post.ID)
	default:
		settle(w.Write, err)
		r.requestError(ctx, u, "%s: 投稿 %s に RealMoji を送れません: %v", u.Username, post.ID, err)
	}
	return false
}

// checkReaction は RealMoji を送った応答が、送った人と種類の、署名された画像の付いた RealMoji で
// あることを確かめ、違えばその内容と、エラー率に数える件数を返す。署名の検証は重いので、sample
// で選んだときだけ行う（見つけた誤りは選んだ割合の逆数の件数に数える）。
func (r *Runner) checkReaction(u *user, kind string, reaction client.RealMoji) (string, int64) {
	switch {
	case reaction.ID == "":
		return "ID がありません", 1
	case reaction.Reactor.ID != u.ID:
		return "送った人が違います", 1
	case reaction.ReactionType != kind:
		return fmt.Sprintf("種類が %q ではありません（%q）", kind, reaction.ReactionType), 1
	case reaction.CreatedAt.IsZero():
		return "作成時刻がありません", 1
	}
	if err := imageurl.CheckForm(reaction.ImageURL); err != nil {
		return err.Error(), 1
	}
	if weight := r.sample(); weight > 0 {
		if err := imageurl.Verify(reaction.ImageURL, imageurl.ImageID(reaction.ImageURL), u.SecretKey, time.Now()); err != nil {
			return err.Error(), weight
		}
	}
	return "", 0
}

// deleteRealMoji は今日送った RealMoji を 1 つ取り消す。
func (r *Runner) deleteRealMoji(ctx context.Context, u *user, s *session) {
	postID := s.myReactions[len(s.myReactions)-1]
	s.myReactions = s.myReactions[:len(s.myReactions)-1]
	id := r.model.ReactionID(postID, u.ID)
	if id == "" {
		return
	}
	w := r.model.BeginDeleteRealMoji(postID, u.ID)
	sent := time.Now()
	err := u.API.DeleteRealMoji(client.MayFailWith(ctx, http.StatusNotFound, http.StatusForbidden), u.Token, id)
	settleRetried(w, err)
	switch {
	case r.bannedErr(u, err):
		// BAN されたユーザーは止める。失敗には数えない。
	case err == nil:
		r.award(u, s.day, score.TagDeleteReaction)
	case isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusForbidden):
		// 投稿が削除されたり、投稿者が先に消したりしていれば正しい拒否。確実に消せたはずの自分の
		// RealMoji の取り消しを拒否したときだけエラーにする。
		if !retried(err) && r.model.RealMojiDeletable(postID, u.ID, id, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 自分の RealMoji %s の取り消しが拒否されました: %v", u.Username, id, err)
		}
	default:
		r.requestError(ctx, u, "%s: RealMoji %s を取り消せません: %v", u.Username, id, err)
	}
}

// listRealMojis は投稿の RealMoji の一覧を読み、bench が送った RealMoji と突き合わせる。
func (r *Runner) listRealMojis(ctx context.Context, u *user, s *session, postID string) {
	sent := time.Now()
	if r.model.CanView(u.ID, postID, r.model.ReadBetween(sent, sent)) != model.True {
		return
	}
	page, err := u.API.RealMojis(client.MayFailWith(ctx, http.StatusForbidden), u.Token, postID, nil)
	read := r.model.ReadBetween(sent, time.Now())
	switch {
	case r.bannedErr(u, err):
		return
	case isStatus(err, http.StatusForbidden):
		if r.model.CanView(u.ID, postID, read) == model.True {
			r.invalid(ctx, u, "%s: 見える投稿 %s の RealMoji の一覧が拒否されました", u.Username, postID)
		}
		return
	case err != nil:
		r.requestError(ctx, u, "%s: 投稿 %s の RealMoji の一覧を取得できません: %v", u.Username, postID, err)
		return
	}
	problems := r.model.CheckRealMojis(u.ID, postID, page, read)
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: 投稿 %s の RealMoji の一覧: %s", u.Username, postID, problem)
	}
	if len(problems) == 0 && s.lists < scoredListsPerDay {
		s.lists++
		r.award(u, s.day, score.TagReactionList)
	}
}

// comment はコメントを送る。送る条件と結果の正否は react と同じく判定する。送れたら true。
func (r *Runner) comment(ctx context.Context, u *user, s *session, post client.FeedPost) bool {
	sent := time.Now()
	if r.model.CanComment(u.ID, post.ID, r.model.ReadBetween(sent, sent)) != model.True {
		return false
	}
	body := u.commentBody()
	creation := r.model.BeginCreateComment(post.ID, u.ID)
	created, err := u.API.CreateComment(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), u.Token, post.ID, body)
	allowed := r.model.CanComment(u.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case r.bannedErr(u, err):
		creation.Rejected()
	case isStatus(err, http.StatusNotFound) && allowed != model.True:
		// 送っている間に投稿者が退会していれば 404 が正しい。
		creation.Rejected()
	case isStatus(err, http.StatusNotFound):
		creation.Rejected()
		r.invalid(ctx, u, "%s: コメントできる投稿 %s へのコメントが 404 になりました", u.Username, post.ID)
	case err == nil && allowed == model.False:
		creation.Unknown()
		r.board.Criticalf("%s: コメントできない投稿 %s にコメントできてしまいました", u.Username, post.ID)
	case err == nil && (created.ID == "" || created.Body != body || created.Author.ID != u.ID || created.CreatedAt.IsZero()):
		creation.Unknown()
		r.invalid(ctx, u, "%s: コメントの応答が送った内容と違います", u.Username)
	case err == nil && !r.model.CreatedComment(creation, created, u.ID):
		creation.Unknown()
		r.invalid(ctx, u, "%s: コメントの応答の ID %s が、ほかのコメントと同じです", u.Username, created.ID)
	case err == nil:
		r.award(u, s.day, score.TagComment)
		s.myComments = append(s.myComments, created.ID)
		return true
	case isStatus(err, http.StatusForbidden) && allowed != model.True:
		creation.Rejected()
	case isStatus(err, http.StatusForbidden):
		creation.Rejected()
		r.invalid(ctx, u, "%s: コメントできる投稿 %s へのコメントが拒否されました", u.Username, post.ID)
	default:
		settle(creation.Write, err)
		r.requestError(ctx, u, "%s: 投稿 %s にコメントできません: %v", u.Username, post.ID, err)
	}
	return false
}

// deleteComment は今日書いたコメントを 1 つ消す。
func (r *Runner) deleteComment(ctx context.Context, u *user, s *session) {
	id := s.myComments[len(s.myComments)-1]
	s.myComments = s.myComments[:len(s.myComments)-1]
	w := r.model.BeginDeleteComment(id)
	sent := time.Now()
	err := u.API.DeleteComment(client.MayFailWith(ctx, http.StatusNotFound, http.StatusForbidden), u.Token, id)
	settleRetried(w, err)
	switch {
	case r.bannedErr(u, err):
	case err == nil:
		r.award(u, s.day, score.TagDeleteReaction)
	case isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusForbidden):
		// RealMoji の取り消しと同じく、確実に消せたはずのときだけエラーにする。
		if !retried(err) && r.model.CommentDeletable(id, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 自分のコメント %s の削除が拒否されました: %v", u.Username, id, err)
		}
	default:
		r.requestError(ctx, u, "%s: コメント %s を消せません: %v", u.Username, id, err)
	}
}

// retried は err が、503 を受けて送り直した要求の応答か。前の試行が反映されていれば
// （削除済みなど）、送り直した要求の拒否は正しい。
func retried(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Attempts > 1
}

// settleRetried は settle と同じく結果を記録するが、503 のあとに送り直して拒否された要求は、
// 前の試行が反映されたかもしれないので結果不明とする。
func settleRetried(w *model.Write, err error) {
	if retried(err) {
		w.Unknown()
		return
	}
	settle(w, err)
}

// listComments は投稿のコメントの一覧を読み、bench が書いたコメントと突き合わせる。
func (r *Runner) listComments(ctx context.Context, u *user, s *session, postID string) {
	sent := time.Now()
	if r.model.CanComment(u.ID, postID, r.model.ReadBetween(sent, sent)) != model.True {
		return
	}
	page, err := u.API.Comments(client.MayFailWith(ctx, http.StatusForbidden), u.Token, postID, nil)
	read := r.model.ReadBetween(sent, time.Now())
	switch {
	case r.bannedErr(u, err):
		return
	case isStatus(err, http.StatusForbidden):
		if r.model.CanComment(u.ID, postID, read) == model.True {
			r.invalid(ctx, u, "%s: コメントできる投稿 %s のコメントの一覧が拒否されました", u.Username, postID)
		}
		return
	case err != nil:
		r.requestError(ctx, u, "%s: 投稿 %s のコメントの一覧を取得できません: %v", u.Username, postID, err)
		return
	}
	problems := r.model.CheckComments(u.ID, postID, nil, page, read)
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: 投稿 %s のコメントの一覧: %s", u.Username, postID, problem)
	}
	if len(problems) == 0 && s.lists < scoredListsPerDay {
		s.lists++
		r.award(u, s.day, score.TagReactionList)
	}
}

// acceptRequests は届いている友達申請を読み、すべて承認する。
func (r *Runner) acceptRequests(ctx context.Context, u *user) {
	page, err := u.API.Relationships(ctx, u.Token, "incoming-requests", nil)
	if err != nil {
		r.requestError(ctx, u, "%s: 届いた友達申請を取得できません: %v", u.Username, err)
		return
	}
	for _, request := range page.Items {
		if r.model.User(request.User.ID) == nil {
			if !r.isEvilUser(request.User.ID) {
				r.invalid(ctx, u, "%s: bench の知らないユーザー %s から友達申請が届いています", u.Username, request.User.ID)
			}
			continue
		}
		r.answerRequest(ctx, u, request, "accept")
	}
}

// answerRequest は届いた申請を承認（accept）するか断る（reject）。
func (r *Runner) answerRequest(ctx context.Context, u *user, request client.Relationship, action string) {
	var w *model.Write
	if action == "accept" {
		w = r.model.BeginFriendship(request.User.ID, u.ID, true)
	}
	closing := r.model.BeginCloseFriendRequest(request.RelationID)
	sent := time.Now()
	// 申請した側が取り下げたり、相手を退会させたりしていれば 403 か 409 になる。
	result, err := u.API.TransitionFriendRequest(client.MayFailWith(ctx, http.StatusForbidden, http.StatusConflict, http.StatusNotFound), u.Token, request.RelationID, action)
	malformed := err == nil && (result.RelationID != request.RelationID || result.Status != map[string]string{"accept": "accepted", "reject": "rejected"}[action])
	if malformed {
		// 承認・拒否できたのかわからない。
		if w != nil {
			w.Unknown()
		}
		closing.Unknown()
	} else {
		if w != nil {
			settleRetried(w, err)
		}
		settleRetried(closing, err)
	}
	switch {
	case r.bannedErr(u, err):
	case malformed:
		r.invalid(ctx, u, "%s: 友達申請 %s を%sした応答が不正です（relationId %q、status %q）", u.Username, request.RelationID, map[string]string{"accept": "承認", "reject": "拒否"}[action], result.RelationID, result.Status)
	case err == nil && action == "accept":
		r.addFriendship(request.User.ID, u.ID)
		r.award(u, r.dayNow(u.Region), score.TagFriendAccept)
	case err == nil:
		r.award(u, r.dayNow(u.Region), score.TagFriendDecline)
	case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusConflict) || isStatus(err, http.StatusNotFound):
		// 送っている間ずっと確実に保留中だった（bench が送って記録した）申請の拒否だけをエラーにする。
		if !retried(err) && r.model.FriendRequestPending(request.RelationID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 保留中の友達申請 %s を%sできません: %v", u.Username, request.RelationID, map[string]string{"accept": "承認", "reject": "拒否"}[action], err)
		}
	default:
		r.requestError(ctx, u, "%s: 友達申請を%sできません: %v", u.Username, map[string]string{"accept": "承認", "reject": "拒否"}[action], err)
	}
}

func (r *Runner) addFriendship(a, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.friendCount[a]++
	r.friendCount[b]++
}

// pickTarget は candidates から友達の数 + 1 に比例した重みで 1 人選ぶ（優先的選択）。
func (r *Runner) pickTarget(u *user, candidates []*user) *user {
	r.mu.Lock()
	weights := make([]int, len(candidates))
	total := 0
	for i, c := range candidates {
		weights[i] = r.friendCount[c.ID] + 1
		total += weights[i]
	}
	r.mu.Unlock()
	x := u.rng.IntN(total)
	for i, w := range weights {
		if x < w {
			return candidates[i]
		}
		x -= w
	}
	return candidates[len(candidates)-1]
}
