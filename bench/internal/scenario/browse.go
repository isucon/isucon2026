package scenario

import (
	"context"
	"net/http"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// feedWeights はフィードを選ぶ重み。
var feedWeights = []struct {
	feed   string
	weight float64
}{{"friends", 0.35}, {"friends-of-friends", 0.25}, {"public", 0.25}, {"following", 0.15}}

// nextPageRate は、フィードに続きがあるときに次のページも読む確率。
const nextPageRate = 0.5

// deletedImageGrace は、投稿を削除してからその画像を返さなくなるまでに許す時間。
const deletedImageGrace = 5 * time.Second

func (u *user) pickFeed() string {
	x := u.rng.Float64()
	for _, f := range feedWeights {
		if x < f.weight {
			return f.feed
		}
		x -= f.weight
	}
	return feedWeights[0].feed
}

// browse は 1 回分の閲覧。ときどき友達申請を見たり、関係やプロフィールを整えたりする。
// フィードを 1 つ選んで読み、見えた投稿を 1 つ開いて、過ごし方に合わせて画像、詳細、
// 投稿者のプロフィール、RealMoji やコメントを見たり送ったりする。
func (r *Runner) browse(ctx context.Context, u *user, s *session) {
	switch x := u.rng.Float64(); {
	case x < 0.08:
		r.acceptRequests(ctx, u)
	case x < 0.11:
		r.social(ctx, u, s)
	case x < 0.13:
		r.groom(ctx, u, s)
	case x < 0.15 && u.cohort:
		// 初期データのユーザーは、ときどき自分の過去の投稿を見返す。
		r.readHistory(ctx, u)
	}
	feed := u.pickFeed()
	page, ok := r.readFeed(ctx, u, feed, nil, r.sample())
	// 続きがあれば、ときどき次のページも読む（スクロール）。
	for ok && page.HasMore && len(page.Items) > 0 && u.rng.Float64() < nextPageRate {
		last := page.Items[len(page.Items)-1]
		page, ok = r.readFeed(ctx, u, feed, &last, r.sample())
	}
	if !ok {
		return
	}
	if s.feeds < scoredFeedsPerDay {
		s.feeds++
		r.board.Add(score.TagFeed)
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
	post := candidates[u.rng.IntN(len(candidates))]
	if u.rng.Float64() < 0.3 {
		r.openPost(ctx, u, s, post)
	}
	if s.images < scoredImagesPerDay && u.rng.Float64() < 0.5 && r.checkImage(ctx, u, post) {
		s.images++
		r.board.Add(score.TagImage)
	}
	if u.rng.Float64() < 0.15 {
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
		r.board.Add(score.TagPostDetail)
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
	default:
		if s.profiles < scoredProfilesPerDy {
			s.profiles++
			r.board.Add(score.TagProfileView)
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
	w := r.model.BeginReplaceRealMoji(post.ID, u.ID)
	reaction, err := u.API.ReplaceRealMoji(client.MayFailWith(ctx, http.StatusForbidden), u.Token, post.ID, dataset.ReactionType(u.rng), r.photos.RealMoji(hashOf(r.seed, "realmoji", u.Username, itoa(s.day), itoa(s.realMojis))))
	allowed := r.model.CanView(u.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case err == nil && allowed == model.False:
		w.Unknown()
		r.board.Criticalf("%s: 見えない投稿 %s に RealMoji を送れてしまいました", u.Username, post.ID)
	case err == nil:
		r.model.ReplacedRealMoji(w, reaction.ID)
		r.board.Add(score.TagRealMoji)
		r.reacted(post.Author.ID, post.PostingWindowID)
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

// deleteRealMoji は今日送った RealMoji を 1 つ取り消す。
func (r *Runner) deleteRealMoji(ctx context.Context, u *user, s *session) {
	postID := s.myReactions[len(s.myReactions)-1]
	s.myReactions = s.myReactions[:len(s.myReactions)-1]
	id := r.model.ReactionID(postID, u.ID)
	if id == "" {
		return
	}
	w := r.model.BeginDeleteRealMoji(postID, u.ID)
	err := u.API.DeleteRealMoji(client.MayFailWith(ctx, http.StatusNotFound, http.StatusForbidden), u.Token, id)
	settle(w, err)
	switch {
	case err == nil:
		r.board.Add(score.TagDeleteReaction)
	case isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusForbidden):
		// 投稿が削除されたり、投稿者が先に消したりしていれば正しい拒否。
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
	case isStatus(err, http.StatusForbidden):
		if r.model.CanView(u.ID, postID, read) == model.True {
			r.invalid(ctx, u, "%s: 見える投稿 %s の RealMoji の一覧が拒否されました", u.Username, postID)
		}
		return
	case err != nil:
		r.requestError(ctx, u, "%s: 投稿 %s の RealMoji の一覧を取得できません: %v", u.Username, postID, err)
		return
	}
	problems := r.model.CheckRealMojis(postID, page, read)
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: 投稿 %s の RealMoji の一覧: %s", u.Username, postID, problem)
	}
	if len(problems) == 0 && s.lists < scoredListsPerDay {
		s.lists++
		r.board.Add(score.TagReactionList)
	}
}

// comment はコメントを送る。送る条件と結果の正否は react と同じく判定する。送れたら true。
func (r *Runner) comment(ctx context.Context, u *user, s *session, post client.FeedPost) bool {
	sent := time.Now()
	if r.model.CanComment(u.ID, post.ID, r.model.ReadBetween(sent, sent)) != model.True {
		return false
	}
	body := dataset.CommentBody(u.rng)
	creation := r.model.BeginCreateComment(post.ID, u.ID)
	created, err := u.API.CreateComment(client.MayFailWith(ctx, http.StatusForbidden), u.Token, post.ID, body)
	allowed := r.model.CanComment(u.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case err == nil && allowed == model.False:
		creation.Unknown()
		r.board.Criticalf("%s: コメントできない投稿 %s にコメントできてしまいました", u.Username, post.ID)
	case err == nil && (created.ID == "" || created.Body != body):
		creation.Unknown()
		r.invalid(ctx, u, "%s: コメントの応答が送った内容と違います", u.Username)
	case err == nil:
		r.model.CreatedComment(creation, created, u.ID)
		r.board.Add(score.TagComment)
		r.reacted(post.Author.ID, post.PostingWindowID)
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
	err := u.API.DeleteComment(client.MayFailWith(ctx, http.StatusNotFound, http.StatusForbidden), u.Token, id)
	settle(w, err)
	switch {
	case err == nil:
		r.board.Add(score.TagDeleteReaction)
	case isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusForbidden):
	default:
		r.requestError(ctx, u, "%s: コメント %s を消せません: %v", u.Username, id, err)
	}
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
	case isStatus(err, http.StatusForbidden):
		if r.model.CanComment(u.ID, postID, read) == model.True {
			r.invalid(ctx, u, "%s: コメントできる投稿 %s のコメントの一覧が拒否されました", u.Username, postID)
		}
		return
	case err != nil:
		r.requestError(ctx, u, "%s: 投稿 %s のコメントの一覧を取得できません: %v", u.Username, postID, err)
		return
	}
	problems := r.model.CheckComments(postID, nil, page, read)
	for _, problem := range problems {
		r.invalid(ctx, u, "%s: 投稿 %s のコメントの一覧: %s", u.Username, postID, problem)
	}
	if len(problems) == 0 && s.lists < scoredListsPerDay {
		s.lists++
		r.board.Add(score.TagReactionList)
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
	// 申請した側が取り下げたり、相手を退会させたりしていれば 403 か 409 になる。
	_, err := u.API.TransitionFriendRequest(client.MayFailWith(ctx, http.StatusForbidden, http.StatusConflict, http.StatusNotFound), u.Token, request.RelationID, action)
	if w != nil {
		settle(w, err)
	}
	switch {
	case err == nil && action == "accept":
		r.addFriendship(request.User.ID, u.ID)
		r.board.Add(score.TagFriendAccept)
	case err == nil:
		r.board.Add(score.TagFriendDecline)
	case isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusConflict) || isStatus(err, http.StatusNotFound):
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

// reacted は authorID の投稿が RealMoji かコメントを受けたことを数える。翌日に、その日に
// 最も反応を集めた人を管理者が認定する。
func (r *Runner) reacted(authorID, windowID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reactionsByWindow[windowID] == nil {
		r.reactionsByWindow[windowID] = map[string]int{}
	}
	r.reactionsByWindow[windowID][authorID]++
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
