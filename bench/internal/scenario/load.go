package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
)

var (
	reactionTypes = []string{"like", "heart", "laugh", "smile", "surprise", "instant"}
	visibilities  = []string{"public", "friends", "friends_of_friends"}
	feeds         = []string{"friends", "public", "friends-of-friends", "following"}
)

// 1 人が 1 つの投稿期間に送る RealMoji とコメントの上限（P-115 §4.2）。
const (
	realMojisPerWindow = 3
	commentsPerWindow  = 3
)

// 1 人が 1 つの投稿期間で加点されるフィードの閲覧と画像の取得の上限。上限を超えても
// 閲覧は続ける（負荷は変わらない）が、加点はしない。閲覧の点を通過数に従属させ、ゲート
// 通過が点の 6 割以上を占めるようにする（P-115 §3.3、§9）。値は較正までの暫定値。
const (
	scoredFeedsPerWindow  = 20
	scoredImagesPerWindow = 20
)

// nextPageRate は、フィードに続きがあるときに次のページも読む確率。
const nextPageRate = 0.5

// deletedImageGrace は、投稿を削除してからその画像を返さなくなるまでに許す時間。
const deletedImageGrace = 5 * time.Second

// runSchedule は時間割に従って region の投稿期間を開く。リージョンの i 番目（0 始まり）は
// 開始から offset*i 後に最初の期間を開き、以後 period ごとに開く。走行の終わりまでに
// 開けなかった期間は開かない。
func (r *Runner) runSchedule(ctx context.Context, start time.Time, region string, index int) string {
	deadline, hasDeadline := ctx.Deadline()
	for k := 0; ; k++ {
		at := start.Add(r.schedule.offset*time.Duration(index) + r.schedule.period*time.Duration(k))
		if hasDeadline && !at.Before(deadline) {
			return ""
		}
		if err := sleepUntil(ctx, at); err != nil {
			return ""
		}
		date := r.firstDate.AddDate(0, 0, k)
		open := r.model.BeginOpenWindow(region)
		window, err := r.admin.OpenWindow(ctx, r.adminToken, region, date.Format(time.DateOnly))
		if err != nil {
			// 開けたかどうかわからない（タイムアウトなど）ので、モデルでは不明のままにする。
			// 管理者は次の予定の時刻に（次の日付で）開き直す。
			settle(open, err)
			r.requestError(ctx, "%s の投稿期間を開けません: %v", region, err)
			continue
		}
		if window.ID == "" || window.Region != region || window.LocalDate != date.Format(time.DateOnly) {
			open.Unknown()
			return fmt.Sprintf("%s の投稿期間を開いた応答が不正です: %+v", region, window)
		}
		r.model.OpenedWindow(open, model.Window{ID: window.ID, Region: region, LocalDate: date})
		r.mu.Lock()
		r.opened[region]++
		r.mu.Unlock()
		r.cfg.Logger.Info("window open", "region", region, "localDate", window.LocalDate)
		r.maybeBuzz(region)
	}
}

func sleepUntil(ctx context.Context, at time.Time) error {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// session はあるユーザーが投稿した投稿期間での状態。
type session struct {
	windowID  string
	realMojis int
	comments  int
	feeds     int
	images    int
}

// runUser は 1 人のユーザーのクローズドループ。同時に 1 リクエストだけ送り、待ち時間は
// 置かない。通知を受けたら投稿してフィードを開き（ゲート通過）、次の通知までフィードを
// 読んで反応し続ける。通知は別の接続（listen）で待つ。join なら、通知を待たずに開いている
// 投稿期間に投稿してから始める（走行の途中で登録したユーザー）。
func (r *Runner) runUser(ctx context.Context, user *agent.Agent, join bool) {
	notifications := make(chan client.Notification, 1)
	go r.listen(ctx, user, notifications)
	var current *session
	var previous *client.Post
	if join {
		status, err := user.API.CurrentWindow(ctx, user.Token)
		switch {
		case err != nil:
			r.requestError(ctx, "%s: 現在の投稿期間を取得できません: %v", user.Username, err)
		case status.Window != nil && status.CanPost:
			current, previous = r.enterWindow(ctx, user, status.Window.ID, previous)
		}
	}
	for ctx.Err() == nil {
		select {
		case n := <-notifications:
			current, previous = r.enterWindow(ctx, user, n.PostingWindowID, previous)
			continue
		default:
		}
		if current == nil {
			select {
			case n := <-notifications:
				current, previous = r.enterWindow(ctx, user, n.PostingWindowID, previous)
			case <-ctx.Done():
			}
			continue
		}
		r.browse(ctx, user, current)
	}
}

// listen は通知の購読を続け、受け取った通知を out に渡す。アプリの通知ストリームは 1 件
// 送ると閉じるので、そのたびに購読し直す。
//
// 通知を待つ間にストリームが切れる（ルーターのタイムアウトなど）のは実際の端末でも起きる
// ので、エラーにせず購読し直す。通知が届かなければゲートを通過できないので、それで数に
// 表れる。すぐに切れ続ける場合に備えて、1 秒未満で切れたら 1 秒待つ。
func (r *Runner) listen(ctx context.Context, user *agent.Agent, out chan client.Notification) {
	for ctx.Err() == nil {
		opened := time.Now()
		stream, err := user.API.OpenNotifications(ctx, user.Token)
		if err != nil {
			if ctx.Err() == nil {
				r.invalid(ctx, user.Region, "%s: 通知を購読できません: %v", user.Username, err)
				_ = sleepUntil(ctx, time.Now().Add(time.Second))
			}
			continue
		}
		n, err := stream.Receive()
		valid := err == nil && n.ID != "" && n.PostingWindowID != "" && n.Region == user.Region
		if valid {
			r.board.Add(score.TagNotification)
			// 古い通知が残っていれば新しい通知で置き換える。
			select {
			case <-out:
			default:
			}
			out <- n
		}
		// 残りを読み切って接続を使い回す。応答を終えない実装でも通知を待たせないよう、
		// 通知を渡してから閉じる。
		_ = stream.Close()
		switch {
		case err != nil:
			if time.Since(opened) < time.Second {
				_ = sleepUntil(ctx, opened.Add(time.Second))
			}
		case !valid:
			r.invalid(ctx, user.Region, "%s: 通知の内容が不正です（リージョン %s）", user.Username, n.Region)
		}
	}
}

// enterWindow は投稿期間 windowID に投稿し、friends フィードを開く。フィードが検証に通れば
// ゲート通過とし、その投稿期間での状態を返す。投稿できなかったか、投稿を削除した場合は
// nil を返し、次の通知まで何もしない。
func (r *Runner) enterWindow(ctx context.Context, user *agent.Agent, windowID string, previous *client.Post) (*session, *client.Post) {
	if _, err := r.model.WaitForWindow(ctx, user.Region, windowID); err != nil {
		if errors.Is(err, model.ErrUnknownWindow) {
			r.invalid(ctx, user.Region, "%s: 開いていない投稿期間 %s が通知されました", user.Username, windowID)
		}
		return nil, previous
	}
	if previous != nil {
		r.checkMemory(ctx, user, previous)
	}
	post, ok := r.post(ctx, user, windowID)
	if !ok {
		return nil, previous
	}
	// ゲート通過は大きな得点なので、欠落もいつも確かめる（friends の候補は索引で引けて軽い）。
	if _, ok := r.readFeed(ctx, user, "friends", nil, 1); ok {
		r.board.Add(score.TagGatePass)
	}
	return &session{windowID: windowID}, &post
}

// browse は 1 回分の閲覧。フィードを 1 つ選んで読み、見えた投稿を 1 つ選んで画像を取得し、
// 上限の範囲で RealMoji とコメントを送る。
func (r *Runner) browse(ctx context.Context, user *agent.Agent, s *session) {
	if rand.Float64() < r.acceptRate {
		r.acceptRequests(ctx, user)
	}
	if r.cohort[user.ID] && rand.Float64() < r.historyRate {
		r.readHistory(ctx, user)
	}
	feed := feeds[rand.IntN(len(feeds))]
	page, ok := r.readFeed(ctx, user, feed, nil, r.sample())
	// 続きがあれば、ときどき次のページも読む（スクロール）。
	for ok && page.HasMore && len(page.Items) > 0 && rand.Float64() < nextPageRate {
		last := page.Items[len(page.Items)-1]
		page, ok = r.readFeed(ctx, user, feed, &last, r.sample())
	}
	if !ok {
		return
	}
	if s.feeds < scoredFeedsPerWindow {
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
	post := candidates[rand.IntN(len(candidates))]
	if r.checkImage(ctx, user, post) && s.images < scoredImagesPerWindow {
		s.images++
		r.board.Add(score.TagImage)
	}
	if s.realMojis < realMojisPerWindow && r.react(ctx, user, post, reactionTypes[rand.IntN(len(reactionTypes))]) {
		s.realMojis++
	}
	if s.comments < commentsPerWindow && r.comment(ctx, user, post, fmt.Sprintf("%s から", user.Username)) {
		s.comments++
	}
}

// post は公開範囲をランダムに選んで windowID の投稿期間に投稿する。投稿後に一部のユーザーは
// 公開範囲を変え、さらに一部は投稿を削除する。削除したら他人の投稿が見えなくなるので、
// その投稿期間ではもう何もしない（false を返す）。
func (r *Runner) post(ctx context.Context, user *agent.Agent, windowID string) (client.Post, bool) {
	status, err := user.API.CurrentWindow(ctx, user.Token)
	if err != nil {
		r.requestError(ctx, "%s: 現在の投稿期間を取得できません: %v", user.Username, err)
		return client.Post{}, false
	}
	if status.Window == nil || status.Window.ID != windowID || !status.CanPost || status.HasPosted {
		// 通知を受けてから次の投稿期間が開くこともあるので、エラーにはしない。
		return client.Post{}, false
	}
	visibility := r.visibility()
	caption := fmt.Sprintf("%s の投稿", user.Username)
	creation := r.model.BeginCreatePost(user.ID)
	post, err := user.API.CreatePost(ctx, user.Token, r.postImage, caption, visibility)
	if err != nil {
		settle(creation.Write, err)
		r.requestError(ctx, "%s: 投稿できません: %v", user.Username, err)
		return client.Post{}, false
	}
	if post.ID == "" || post.Author.ID != user.ID || post.Visibility != visibility || post.Caption != caption || post.PostedAt.IsZero() {
		creation.Unknown()
		r.invalid(ctx, user.Region, "%s: 投稿の応答が投稿した内容と違います", user.Username)
		return client.Post{}, false
	}
	// 確認のあとに次の投稿期間が開けば、投稿はそちらに入る。どちらでも bench が開いた
	// 投稿期間であればよい。
	if _, err := r.model.WaitForWindow(ctx, user.Region, post.PostingWindowID); err != nil {
		creation.Unknown()
		if errors.Is(err, model.ErrUnknownWindow) {
			r.invalid(ctx, user.Region, "%s: 投稿が開いていない投稿期間 %s に入りました", user.Username, post.PostingWindowID)
		}
		return client.Post{}, false
	}
	r.model.Created(creation, model.Post{
		ID: post.ID, AuthorID: user.ID, WindowID: post.PostingWindowID, ImageID: imageurl.ImageID(post.ImageURL),
		Caption: post.Caption, PostedAt: post.PostedAt,
	}, visibility)
	if !r.checkImage(ctx, user, post.FeedPost()) {
		return client.Post{}, false
	}
	r.board.Add(score.TagImage)

	switch n := rand.Float64(); {
	case n < r.deleteRate:
		w := r.model.BeginDeletePost(post.ID)
		err := user.API.DeletePost(ctx, user.Token, post.ID)
		settle(w, err)
		if err != nil {
			r.requestError(ctx, "%s: 投稿を削除できません: %v", user.Username, err)
		}
		return client.Post{}, false
	case n < r.deleteRate+r.changeRate:
		next := visibilities[(slices.Index(visibilities, visibility)+1+rand.IntN(2))%len(visibilities)]
		w := r.model.BeginUpdateVisibility(post.ID, next)
		sent := time.Now()
		updated, err := user.API.UpdatePostVisibility(client.MayFailWith(ctx, http.StatusForbidden), user.Token, post.ID, next)
		settle(w, err)
		switch {
		case isStatus(err, http.StatusForbidden):
			// 投稿してから変えるまでに次の投稿期間が開けば、編集できないのが正しい。
			if r.model.WindowOpen(post.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
				r.requestError(ctx, "%s: 投稿の公開範囲を変えられません: %v", user.Username, err)
			}
		case err != nil:
			r.requestError(ctx, "%s: 投稿の公開範囲を変えられません: %v", user.Username, err)
		case updated.Visibility != next:
			r.invalid(ctx, user.Region, "%s: 公開範囲を変えた応答が違います", user.Username)
		}
	}
	return post, true
}

// checkImage は投稿の画像 URL が投稿者の署名したものであり、中身が投稿した画像と一致する
// ことを確かめる。署名の検証（HMAC を 1000 回）は重いので、sample で選んだときだけ行う。
func (r *Runner) checkImage(ctx context.Context, user *agent.Agent, post client.FeedPost) bool {
	author := r.model.User(post.Author.ID)
	if author == nil {
		r.invalid(ctx, user.Region, "%s: 投稿 %s の投稿者を bench が知りません", user.Username, post.ID)
		return false
	}
	if weight := r.sample(); weight > 0 {
		if err := imageurl.Verify(post.ImageURL, imageurl.ImageID(post.ImageURL), author.SecretKey, time.Now()); err != nil {
			r.invalidN(ctx, user.Region, weight, "%s: 投稿 %s の画像 URL が不正です: %v", user.Username, post.ID, err)
			return false
		}
	}
	// 削除された投稿の画像は取得できない。取得している間に削除されることもあるので、
	// 403 は、その区間で投稿が確実に残っていた場合だけエラーにする。
	sent := time.Now()
	image, err := user.API.Image(client.MayFailWith(ctx, http.StatusForbidden), post.ImageURL)
	if isStatus(err, http.StatusForbidden) {
		if r.model.PostLive(post.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, user.Region, "%s: 削除されていない投稿 %s の画像が拒否されました", user.Username, post.ID)
		}
		return false
	}
	if err != nil {
		r.requestError(ctx, "%s: 投稿 %s の画像を取得できません: %v", user.Username, post.ID, err)
		return false
	}
	// 削除から deletedImageGrace を過ぎても画像を返すのは違反。
	grace := model.Read{Sent: sent, Received: time.Now(), Staleness: deletedImageGrace}
	if r.model.PostLive(post.ID, grace) == model.False {
		r.board.Criticalf("%s: 削除から %s 以上経った投稿 %s の画像が取得できました", user.Username, deletedImageGrace, post.ID)
		return false
	}
	if !bytes.Equal(image, r.postImage) {
		r.invalid(ctx, user.Region, "%s: 投稿 %s の画像が投稿した画像と違います", user.Username, post.ID)
		return false
	}
	return true
}

// readFeed はフィードの before より古い投稿のページ（before が nil なら最初のページ）を読み、
// モデルで検証する。検証に通れば true。weight が正なら欠落と署名も確かめ（thorough）、それで
// 見つけた失敗 1 件をエラー率に weight 件として数える。
func (r *Runner) readFeed(ctx context.Context, user *agent.Agent, feed string, before *client.FeedPost, weight int64) (client.FeedPage, bool) {
	sent := time.Now()
	page, err := user.API.Feed(ctx, user.Token, feed, before)
	if err != nil {
		r.requestError(ctx, "%s: フィード %s を取得できません: %v", user.Username, feed, err)
		return page, false
	}
	result := r.model.CheckFeed(user.ID, feed, before, page, r.model.ReadBetween(sent, time.Now()), weight > 0)
	for _, violation := range result.Violations {
		r.board.Criticalf("%s: %s", user.Username, violation)
	}
	for _, message := range result.Errors {
		r.invalid(ctx, user.Region, "%s: %s", user.Username, message)
	}
	for _, message := range result.Thorough {
		r.invalidN(ctx, user.Region, weight, "%s: %s", user.Username, message)
	}
	return page, len(result.Violations)+len(result.Errors)+len(result.Thorough) == 0
}

// react は RealMoji を送る。送ってよいとモデルが判定した投稿にだけ送る。送っている間に
// 投稿が見えなくなることもあるので、結果の正否は送った区間でのモデルの判定と比べる。
// 見えるはずなのに拒否されたらエラー、見えないはずなのに受け付けられたら違反。送れたら true。
func (r *Runner) react(ctx context.Context, user *agent.Agent, post client.FeedPost, reactionType string) bool {
	sent := time.Now()
	if r.model.CanView(user.ID, post.ID, r.model.ReadBetween(sent, sent)) != model.True {
		return false
	}
	err := user.API.ReplaceRealMoji(client.MayFailWith(ctx, http.StatusForbidden), user.Token, post.ID, reactionType, r.mojiImage)
	allowed := r.model.CanView(user.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case err == nil && allowed == model.False:
		r.board.Criticalf("%s: 見えない投稿 %s に RealMoji を送れてしまいました", user.Username, post.ID)
	case err == nil:
		r.board.Add(score.TagRealMoji)
		return true
	case isStatus(err, http.StatusForbidden) && allowed != model.True:
	case isStatus(err, http.StatusForbidden):
		r.invalid(ctx, user.Region, "%s: 見える投稿 %s への RealMoji が拒否されました", user.Username, post.ID)
	default:
		r.requestError(ctx, "%s: 投稿 %s に RealMoji を送れません: %v", user.Username, post.ID, err)
	}
	return false
}

// comment はコメントを送る。送る条件と結果の正否は react と同じく判定する。送れたら true。
func (r *Runner) comment(ctx context.Context, user *agent.Agent, post client.FeedPost, body string) bool {
	sent := time.Now()
	if r.model.CanComment(user.ID, post.ID, r.model.ReadBetween(sent, sent)) != model.True {
		return false
	}
	err := user.API.CreateComment(client.MayFailWith(ctx, http.StatusForbidden), user.Token, post.ID, body)
	allowed := r.model.CanComment(user.ID, post.ID, r.model.ReadBetween(sent, time.Now()))
	switch {
	case err == nil && allowed == model.False:
		r.board.Criticalf("%s: コメントできない投稿 %s にコメントできてしまいました", user.Username, post.ID)
	case err == nil:
		r.board.Add(score.TagComment)
		return true
	case isStatus(err, http.StatusForbidden) && allowed != model.True:
	case isStatus(err, http.StatusForbidden):
		r.invalid(ctx, user.Region, "%s: コメントできる投稿 %s へのコメントが拒否されました", user.Username, post.ID)
	default:
		r.requestError(ctx, "%s: 投稿 %s にコメントできません: %v", user.Username, post.ID, err)
	}
	return false
}

// readHistory は cohort のユーザーが、履歴のある月の Memories を読んで初期データと比べる。
// 負荷走行で開いた投稿期間の投稿は比べない（作成の結果がわからず、bench が ID を知らない
// 投稿も含む）。
func (r *Runner) readHistory(ctx context.Context, user *agent.Agent) {
	months := r.initial.HistoryMonths()
	month := months[rand.IntN(len(months))]
	err := r.initial.CheckMemories(ctx, user, month, func(p client.Post) bool { return r.model.HasWindow(p.PostingWindowID) }, r.historyImages, r.sample() > 0)
	r.recordHistory(ctx, user, err)
}

// checkMemory は前回の投稿が、その月の Memories に入っていることを確かめる。
func (r *Runner) checkMemory(ctx context.Context, user *agent.Agent, previous *client.Post) {
	p := r.model.Post(previous.ID)
	if p == nil {
		return
	}
	month := p.LocalDate.Format("2006-01")
	var before *client.Post
	seen := make(map[string]bool)
	for {
		page, err := user.API.MemoriesMonth(ctx, user.Token, month, before)
		if err != nil {
			r.requestError(ctx, "%s: Memories を取得できません: %v", user.Username, err)
			return
		}
		for _, post := range page.Items {
			if seen[post.ID] {
				r.invalid(ctx, user.Region, "%s: Memories に投稿 %s が重複しています", user.Username, post.ID)
				return
			}
			seen[post.ID] = true
			if post.ID == previous.ID {
				r.board.Add(score.TagMemory)
				return
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			r.invalid(ctx, user.Region, "%s: Memories（%s）に前回の投稿 %s がありません", user.Username, month, previous.ID)
			return
		}
		last := page.Items[len(page.Items)-1].Post
		before = &last
	}
}
