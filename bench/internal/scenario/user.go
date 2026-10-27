package scenario

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// activity は投稿したあとの過ごし方。どのユーザーも 1 日 1 回は投稿する（投稿しないと
// 他人の投稿が見えない）ので、違いは投稿のあとに出る。
type activity int

const (
	// postOnly は投稿して友達のフィードを 1 回見たら、次の日まで何もしない。
	postOnly activity = iota
	// browser はフィードを読み、投稿を開き、画像やプロフィールを見る。
	browser
	// reactor は閲覧に加えて RealMoji を送る。
	reactor
	// commenter は閲覧に加えてコメントを書く。
	commenter
)

func (a activity) String() string {
	return [...]string{"post-only", "browser", "realmoji", "comment"}[a]
}

// activityOf は bench が登録したユーザーの過ごし方を走行の種と名前から決める。初期データの
// 活発さ（各日に投稿する確率 0.9 / 0.5 / 0.1 の 1:4:5）に合わせる。
func activityOf(seed uint64, username string) activity {
	switch u := unit(seed, "activity", username); {
	case u < 0.4:
		return postOnly
	case u < 0.7:
		return browser
	case u < 0.85:
		return reactor
	default:
		return commenter
	}
}

// cohortActivity は初期データのユーザーの活発さから過ごし方を決める。
func cohortActivity(seed uint64, username string, active float64) activity {
	switch {
	case active < 0.3:
		return postOnly
	case active < 0.7:
		return browser
	case unit(seed, "activity", username) < 0.5:
		return reactor
	default:
		return commenter
	}
}

// user は負荷走行で動くユーザー（初期の登録ユーザー、初期データの cohort、流入したユーザー、
// インフルエンサー）。rng と日ごとの状態は、そのユーザーのゴルーチンだけが使う。
type user struct {
	*agent.Agent
	activity activity
	// persona は好みと癖。
	persona persona
	// registered は bench が登録したユーザー。プロフィール画像を変えてよい（初期データの
	// ユーザーの画像を消すと initialize で戻せない）。
	registered bool
	cohort     bool
	// certified は認定公開アカウント。公開範囲 public で投稿し、フォローされる。
	certified atomic.Bool
	// stopped は退会したか BAN されて、もう動かないユーザー。
	stopped atomic.Bool
	// banned は BAN されて止まったか。BAN されたユーザーは使えなかった人として成長に数え続ける。
	banned atomic.Bool
	// returning は思い出を見返すユーザー（成長の判定に数えない）。
	returning bool
	// leaveOn はこの日に投稿したあとで退会する（0 なら退会しない）。
	leaveOn int
	rng     *rand.Rand

	// profileImage はプロフィール画像の SHA-256（なければ nil）。
	profileImage *[32]byte
	// blocking はこのユーザーが走行中にブロックした相手。
	blocking []string
	// follows はフォローしている認定公開アカウント。
	follows map[string]bool
}

func (r *Runner) newUser(a *agent.Agent, act activity) *user {
	key := hashOf(r.seed, "user", a.Username)
	a.API = a.API.WithClientIP(r.ips.Next())
	return &user{Agent: a, activity: act, persona: newPersona(r.seed, a.Username), rng: rand.New(rand.NewPCG(key, key^0x75736572)), follows: map[string]bool{}}
}

// session はユーザーのある 1 日（投稿期間）の状態。
type session struct {
	windowID string
	day      int
	postID   string
	// budget は残りの閲覧の回数。使い切ったら次の日まで何もしない。
	budget int
	// done はその日を過ごし終えたか（閲覧の回数を使い切ったあとの行動を 1 回だけにする）。
	done bool
	// 加点した回数。上限を超えて閲覧しても加点しない。
	feeds, images, details, lists, profiles int
	realMojis, comments                     int
	myReactions                             []string
	myComments                              []string
}

// dailyBudget は過ごし方ごとの 1 日の閲覧の回数。
func dailyBudget(a activity) int {
	switch a {
	case postOnly:
		return 0
	case browser:
		return 20
	default:
		return 15
	}
}

// 1 日に加点する回数の上限。
const (
	scoredFeedsPerDay   = 20
	scoredImagesPerDay  = 4
	scoredDetailsPerDay = 8
	scoredListsPerDay   = 8
	scoredProfilesPerDy = 4
	realMojisPerDay     = 5
	commentsPerDay      = 5
)

// runUser はユーザーのクローズドループ。通知を受けたら少し待って投稿し、友達のフィードを
// 開き（ゲート通過）、その日の閲覧の回数を使い切るか次の通知が来るまで閲覧する。同時に
// 1 リクエストだけ送る。join なら通知を待たずに開いている投稿期間に投稿する（走行の途中で
// 登録したユーザー）。
func (r *Runner) runUser(ctx context.Context, u *user, join bool, subscribed chan<- struct{}) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	notifications := make(chan notice, 1)
	stopListening := r.startListening(ctx, u, notifications, subscribed)
	defer func() { stopListening() }()
	var s *session
	var previous *client.Post
	if join {
		status, err := u.API.CurrentWindow(ctx, u.Token)
		switch {
		case err != nil:
			r.requestError(ctx, u, "%s: 現在の投稿期間を取得できません: %v", u.Username, err)
		case status.Window != nil && status.CanPost:
			s, previous = r.enterWindow(ctx, u, status.Window.ID, previous, time.Time{}, notifications)
		}
	}
	for ctx.Err() == nil && !u.stopped.Load() {
		select {
		case n := <-notifications:
			s, previous = r.enterWindow(ctx, u, n.PostingWindowID, previous, n.received, notifications)
			continue
		default:
		}
		if s != nil && s.budget <= 0 && !s.done {
			// その日を過ごし終えた。旅に出る人は、ここで別のリージョンへ移る。
			s.done = true
			if r.wandersOff(u, s.day) {
				stopListening = r.move(ctx, u, stopListening, notifications)
				if status, err := u.API.CurrentWindow(ctx, u.Token); err == nil && status.Window != nil && status.CanPost && r.model.HasWindow(status.Window.ID) {
					// 移った先でまだ投稿していない日付の投稿期間が開いていれば、通知を待たずに投稿する。
					s, previous = r.enterWindow(ctx, u, status.Window.ID, previous, time.Time{}, notifications)
				} else if err != nil {
					r.requestError(ctx, u, "%s: 現在の投稿期間を取得できません: %v", u.Username, err)
				}
				continue
			}
		}
		if s == nil || s.budget <= 0 {
			select {
			case n := <-notifications:
				s, previous = r.enterWindow(ctx, u, n.PostingWindowID, previous, n.received, notifications)
			case <-ctx.Done():
			}
			continue
		}
		s.budget--
		r.browse(ctx, u, s)
	}
}

// notice は受け取った通知と、受け取った時刻。
type notice struct {
	client.Notification
	received time.Time
}

// listen は通知の購読を続け、受け取った通知を out に渡す。アプリの通知ストリームは 1 件
// 送ると閉じるので、そのたびに購読し直す。最初の購読ができたら subscribed に知らせる。
//
// 通知を待つ間にストリームが切れる（ルーターのタイムアウトなど）のは実際の端末でも起きる
// ので、エラーにせず購読し直す。通知が届かなければゲートを通過できないので、それで数に
// 表れる。すぐに切れ続ける場合に備えて、1 秒未満で切れたら 1 秒待つ。
func (r *Runner) listen(ctx context.Context, u *user, out chan notice, subscribed chan<- struct{}) {
	// 同じ投稿期間の通知は 1 回だけ数えて渡す。購読し直すたびに同じ通知を返す実装に、
	// 通知の点を繰り返し与えない。
	seen := map[string]bool{}
	for ctx.Err() == nil && !u.stopped.Load() {
		opened := time.Now()
		stream, err := u.API.OpenNotifications(ctx, u.Token)
		if subscribed != nil {
			subscribed <- struct{}{}
			subscribed = nil
		}
		if err != nil {
			if ctx.Err() == nil && !u.stopped.Load() && !r.bannedErr(u, err) {
				r.invalid(ctx, u, "%s: 通知を購読できません: %v", u.Username, err)
				_ = sleepUntil(ctx, time.Now().Add(time.Second))
			}
			continue
		}
		n, err := stream.Receive()
		received := time.Now()
		valid := err == nil && n.ID != "" && n.PostingWindowID != "" && n.Region == u.Region
		if valid && seen[n.PostingWindowID] {
			_ = stream.Close()
			continue
		}
		if valid {
			seen[n.PostingWindowID] = true
			r.award(u, r.dayNow(u.Region), score.TagNotification)
			// 古い通知が残っていれば新しい通知で置き換える。
			select {
			case <-out:
			default:
			}
			out <- notice{Notification: n, received: received}
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
			r.invalid(ctx, u, "%s: 通知の内容が不正です（リージョン %s）", u.Username, n.Region)
		}
	}
}

// enterWindow は投稿期間 windowID に投稿し、友達のフィードを開く。通知を受けた（notified が
// 受けた時刻）なら、ユーザーごと・日ごとに決まる時間だけ待ってから投稿する。フィードが検証に通れば
// ゲート通過とし、その日の状態を返す。投稿できなかったか、投稿を削除した場合は nil を返す。
// 待っている間に次の通知が来たら、何もせずに戻る。
//
// 快適かどうかは、通知がすぐに届いていたら投稿を始めたはずの時刻（投稿期間を開いた時刻 +
// 待ち）から数える。通知の遅れも、投稿とフィードの遅れと同じくユーザーを待たせる。
func (r *Runner) enterWindow(ctx context.Context, u *user, windowID string, previous *client.Post, notified time.Time, notifications chan notice) (*session, *client.Post) {
	if _, err := r.model.WaitForWindow(ctx, u.Region, windowID); err != nil {
		// 開いた結果のわからなかった投稿期間（ErrMaybeWindow）の通知は正しいかもしれないが、
		// bench はその投稿期間を知らないので投稿しない。
		if errors.Is(err, model.ErrUnknownWindow) {
			r.invalid(ctx, u, "%s: 開いていない投稿期間 %s が通知されました", u.Username, windowID)
		}
		return nil, previous
	}
	var delay time.Duration
	intended := time.Now()
	if !notified.IsZero() {
		day := r.dayOfWindow(windowID)
		if !r.attends(u, day) {
			// 今日は投稿しない（休む）。投稿しないとほかの人の投稿は見えないので、何もしない。
			// 自分で休んだ人は、通過できなかった人には数えない。
			r.books.Load().excuse(u.Region, day, u.ID)
			return nil, previous
		}
		delay = r.postDelayOf(u, day)
		intended = r.windowOpenedAt(windowID, intended).Add(delay)
		// 一部のユーザーは投稿する前にフィードを覗く。今日まだ投稿していないので、ほかの
		// 人の投稿は見えてはいけない（見えれば読み取りの検証が違反にする）。
		if unit(r.seed, "peek", u.Username, itoa(day)) < r.peekRate {
			r.readFeed(ctx, u, "friends", nil, 0)
		}
		// 待ちは通知を受けた時刻から数える。bench が投稿期間を知るまでの待ちと覗いた時間は
		// 通過までの時間に足さない。
		delay = max(delay-time.Since(notified), 0)
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case n := <-notifications:
			timer.Stop()
			notifications <- n
			return nil, previous
		case <-ctx.Done():
			timer.Stop()
			return nil, previous
		}
	}
	post, visibility, ok := r.post(ctx, u, windowID)
	if !ok {
		return nil, previous
	}
	day := r.dayOfWindow(post.PostingWindowID)
	// ゲート通過は成長に効くので、欠落もいつも確かめる（friends の候補は索引で引けて軽い）。
	// その日のうちにフィードを開けた場合だけ通過とする。
	if _, ok := r.readFeed(ctx, u, "friends", nil, 1); ok && !r.dayEnded(u.Region, day) {
		r.gatePassed(u, day, time.Since(intended))
	}
	// 投稿してフィードを見たあとで、投稿を確かめたり直したりする。
	if previous != nil {
		r.checkMemory(ctx, u, previous)
	}
	if !r.afterPost(ctx, u, post, visibility) {
		// 撮り直す人は、消したあとでもう一度だけ投稿する（同じ日付の再投稿は 1 回まで）。
		// 撮り直さなければ、削除した投稿は Memories に残らないので、前回の投稿は変えない。
		if u.rng.Float64() >= u.persona.retake {
			return nil, previous
		}
		retaken, _, ok := r.post(ctx, u, post.PostingWindowID)
		if !ok {
			return nil, previous
		}
		r.checkMyCurrentPost(ctx, u, retaken)
		post = retaken
	}
	s := &session{windowID: post.PostingWindowID, day: day, postID: post.ID, budget: budgetOf(u)}
	if u.activity == postOnly && u.rng.Float64() < 0.5 {
		// 投稿だけするユーザーも、ときどき届いた友達申請を見る。
		r.acceptRequests(ctx, u)
	}
	if u.leaveOn == day {
		r.deleteAccount(ctx, u)
	}
	return s, &post
}

// visibilityOf は投稿の公開範囲を選ぶ。認定公開アカウントは public、ほかのユーザーは
// friends か friends_of_friends（一般のユーザーは公開の投稿をしない）。
func (r *Runner) visibilityOf(u *user) string {
	if r.visibility != nil {
		return r.visibility()
	}
	if u.certified.Load() {
		return "public"
	}
	if u.rng.Float64() < u.persona.friendsOnly {
		return "friends"
	}
	return "friends_of_friends"
}

// post は windowID の投稿期間に写真を投稿する。投稿できたら投稿と公開範囲を返す。
func (r *Runner) post(ctx context.Context, u *user, windowID string) (client.Post, string, bool) {
	status, err := u.API.CurrentWindow(ctx, u.Token)
	if err != nil {
		r.requestError(ctx, u, "%s: 現在の投稿期間を取得できません: %v", u.Username, err)
		return client.Post{}, "", false
	}
	if status.Window == nil || status.Window.ID != windowID || !status.CanPost || status.HasPosted {
		// 通知を受けてから次の投稿期間が開くこともあるので、エラーにはしない。
		return client.Post{}, "", false
	}
	visibility := r.visibilityOf(u)
	image, caption := r.photos.Post(u.Region, hashOf(r.seed, "post", u.Username, itoa(r.dayOfWindow(windowID))))
	location := r.locationOf(u, r.dayOfWindow(windowID))
	creation := r.model.BeginCreatePost(u.ID)
	post, err := u.API.CreatePost(client.MayFailWith(ctx, http.StatusConflict), u.Token, image, caption, visibility, location)
	if isStatus(err, http.StatusConflict) {
		creation.Rejected()
		// 確かめてから送るまでに次の投稿期間が開き、その日付にはもう投稿していた（リージョンを
		// 移った人に起きる）なら、投稿できないのが正しい。
		if r.currentWindowID(u.Region) == windowID {
			r.invalid(ctx, u, "%s: 投稿できるはずの投稿期間で投稿が拒否されました: %v", u.Username, err)
		}
		return client.Post{}, "", false
	}
	if err != nil {
		settle(creation.Write, err)
		r.requestError(ctx, u, "%s: 投稿できません: %v", u.Username, err)
		return client.Post{}, "", false
	}
	if post.ID == "" || post.Author.ID != u.ID || post.Visibility != visibility || post.Caption != caption || post.PostedAt.IsZero() || !sameLocation(location, post.Location) {
		creation.Unknown()
		r.invalid(ctx, u, "%s: 投稿の応答が投稿した内容と違います", u.Username)
		return client.Post{}, "", false
	}
	// 確認のあとに次の投稿期間が開けば、投稿はそちらに入る。どちらでも bench が開いた
	// 投稿期間であればよい。
	if _, err := r.model.WaitForWindow(ctx, u.Region, post.PostingWindowID); err != nil {
		creation.Unknown()
		if errors.Is(err, model.ErrUnknownWindow) {
			r.invalid(ctx, u, "%s: 投稿が開いていない投稿期間 %s に入りました", u.Username, post.PostingWindowID)
		}
		return client.Post{}, "", false
	}
	r.model.Created(creation, model.Post{
		ID: post.ID, AuthorID: u.ID, WindowID: post.PostingWindowID, ImageID: imageurl.ImageID(post.ImageURL),
		ImageHash: sha256.Sum256(image), Caption: post.Caption, PostedAt: post.PostedAt,
	}, visibility)
	return post, visibility, true
}

// afterPost は投稿したあと、今日の投稿と画像を確かめる。一部のユーザーは公開範囲を変え、
// さらに一部は投稿を削除する。削除したら他人の投稿が見えなくなるので、その投稿期間では
// もう何もしない（false を返す）。
func (r *Runner) afterPost(ctx context.Context, u *user, post client.Post, visibility string) bool {
	r.checkMyCurrentPost(ctx, u, post)
	if r.checkImage(ctx, u, post.FeedPost()) {
		r.award(u, r.dayNow(u.Region), score.TagImage)
	}
	switch n := u.rng.Float64(); {
	case n < r.deleteRate:
		w := r.model.BeginDeletePost(post.ID)
		err := u.API.DeletePost(ctx, u.Token, post.ID)
		settle(w, err)
		if err != nil {
			r.requestError(ctx, u, "%s: 投稿を削除できません: %v", u.Username, err)
		} else {
			r.recheckDeletedImage(ctx, u, post)
		}
		return false
	case n < r.deleteRate+r.changeRate && !u.certified.Load():
		next := "friends"
		if visibility == "friends" {
			next = "friends_of_friends"
		}
		w := r.model.BeginUpdateVisibility(post.ID, next)
		sent := time.Now()
		updated, err := u.API.UpdatePostVisibility(client.MayFailWith(ctx, http.StatusForbidden), u.Token, post.ID, next)
		settle(w, err)
		switch {
		case isStatus(err, http.StatusForbidden):
			// 投稿してから変えるまでに次の投稿期間が開けば、編集できないのが正しい。
			if r.model.WindowOpen(post.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
				r.requestError(ctx, u, "%s: 投稿の公開範囲を変えられません: %v", u.Username, err)
			}
		case err != nil:
			r.requestError(ctx, u, "%s: 投稿の公開範囲を変えられません: %v", u.Username, err)
		case updated.Visibility != next:
			r.invalid(ctx, u, "%s: 公開範囲を変えた応答が違います", u.Username)
		}
	}
	return true
}

// checkMyCurrentPost は投稿した直後に、自分の今日の投稿が今の投稿であることを確かめる。
func (r *Runner) checkMyCurrentPost(ctx context.Context, u *user, post client.Post) {
	sent := time.Now()
	current, err := u.API.MyCurrentPost(client.MayFailWith(ctx, http.StatusNotFound), u.Token)
	read := r.model.ReadBetween(sent, time.Now())
	switch {
	case isStatus(err, http.StatusNotFound):
		if r.model.PostLive(post.ID, read) == model.True && r.model.WindowOpen(post.ID, read) == model.True {
			r.invalid(ctx, u, "%s: 投稿したのに今日の投稿が 404 になりました", u.Username)
		}
	case err != nil:
		r.requestError(ctx, u, "%s: 今日の投稿を取得できません: %v", u.Username, err)
	case current.ID != post.ID:
		if r.model.WindowOpen(post.ID, read) == model.True {
			r.invalid(ctx, u, "%s: 今日の投稿が投稿した投稿 %s と違います（%s）", u.Username, post.ID, current.ID)
		}
	default:
		r.award(u, r.dayNow(u.Region), score.TagPostDetail)
	}
}

// checkImage は投稿の画像 URL が投稿者の署名したものであり、中身が投稿した画像と一致する
// ことを確かめる。署名の検証（HMAC を 1000 回）は重いので、sample で選んだときだけ行う。
func (r *Runner) checkImage(ctx context.Context, u *user, post client.FeedPost) bool {
	author := r.model.User(post.Author.ID)
	known := r.model.Post(post.ID)
	if author == nil || known == nil {
		r.invalid(ctx, u, "%s: 投稿 %s の投稿者を bench が知りません", u.Username, post.ID)
		return false
	}
	if weight := r.sample(); weight > 0 {
		if err := imageurl.Verify(post.ImageURL, imageurl.ImageID(post.ImageURL), author.SecretKey, time.Now()); err != nil {
			r.invalidN(ctx, u, weight, "%s: 投稿 %s の画像 URL が不正です: %v", u.Username, post.ID, err)
			return false
		}
	}
	// 削除された投稿の画像は取得できない。取得している間に削除されることもあるので、
	// 403 は、その区間で投稿が確実に残っていた場合だけエラーにする。
	sent := time.Now()
	image, err := u.API.Image(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), post.ImageURL)
	if isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusNotFound) {
		if r.model.ImageServable(post.ID, r.model.ReadBetween(sent, time.Now())) == model.True {
			r.invalid(ctx, u, "%s: 削除されていない投稿 %s の画像が拒否されました", u.Username, post.ID)
		}
		return false
	}
	if err != nil {
		r.requestError(ctx, u, "%s: 投稿 %s の画像を取得できません: %v", u.Username, post.ID, err)
		return false
	}
	// 削除から deletedImageGrace を過ぎても画像を返すのは違反。
	grace := model.Read{Sent: sent, Received: time.Now(), Staleness: deletedImageGrace}
	if r.model.PostLive(post.ID, grace) == model.False {
		r.board.Criticalf("%s: 削除から %s 以上経った投稿 %s の画像が取得できました", u.Username, deletedImageGrace, post.ID)
		return false
	}
	if sha256.Sum256(image) != known.ImageHash {
		r.invalid(ctx, u, "%s: 投稿 %s の画像が投稿した画像と違います", u.Username, post.ID)
		return false
	}
	return true
}

// readFeed はフィードの before より古い投稿のページ（before が nil なら最初のページ）を読み、
// モデルで検証する。検証に通れば true。weight が正なら欠落と署名も確かめ（thorough）、それで
// 見つけた失敗 1 件をエラー率に weight 件として数える。
func (r *Runner) readFeed(ctx context.Context, u *user, feed string, before *client.FeedPost, weight int64) (client.FeedPage, bool) {
	sent := time.Now()
	page, err := u.API.Feed(ctx, u.Token, feed, before)
	if err != nil {
		r.requestError(ctx, u, "%s: フィード %s を取得できません: %v", u.Username, feed, err)
		return page, false
	}
	result := r.model.CheckFeed(u.ID, feed, before, page, r.model.ReadBetween(sent, time.Now()), weight > 0)
	for _, violation := range result.Violations {
		r.board.Criticalf("%s: %s", u.Username, violation)
	}
	for _, message := range result.Errors {
		r.invalid(ctx, u, "%s: %s", u.Username, message)
	}
	for _, message := range result.Thorough {
		r.invalidN(ctx, u, weight, "%s: %s", u.Username, message)
	}
	return page, len(result.Violations)+len(result.Errors)+len(result.Thorough) == 0
}

// checkMemory は前回の投稿が、その月の Memories に入っていることを確かめる。
func (r *Runner) checkMemory(ctx context.Context, u *user, previous *client.Post) {
	p := r.model.Post(previous.ID)
	if p == nil {
		return
	}
	month := p.LocalDate.Format("2006-01")
	var before *client.Post
	seen := make(map[string]bool)
	for {
		page, err := u.API.MemoriesMonth(ctx, u.Token, month, before)
		if err != nil {
			r.requestError(ctx, u, "%s: Memories を取得できません: %v", u.Username, err)
			return
		}
		for _, post := range page.Items {
			if seen[post.ID] {
				r.invalid(ctx, u, "%s: Memories に投稿 %s が重複しています", u.Username, post.ID)
				return
			}
			seen[post.ID] = true
			if post.ID == previous.ID {
				r.award(u, r.dayNow(u.Region), score.TagMemory)
				return
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			r.invalid(ctx, u, "%s: Memories（%s）に前回の投稿 %s がありません", u.Username, month, previous.ID)
			return
		}
		last := page.Items[len(page.Items)-1].Post
		before = &last
	}
}

// currentWindowID は bench が region で最後に開いた投稿期間。
func (r *Runner) currentWindowID(region string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastWindow[region]
}

// dayOfWindow は投稿期間がどの日のものか（bench が開いた期間でなければ 0）。
func (r *Runner) dayOfWindow(windowID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.windowDay[windowID]
}

// dayEnded は region の day 日目がもう終わったか（次の日が始まったか、走行が終わったか）。
func (r *Runner) dayEnded(region string, day int) bool {
	b := r.books.Load()
	return b == nil || time.Since(b.start) >= r.tt.end(region, day)
}

// gatePassed は u がその日のゲートを通過したことを数える。グランプリの日は別に数える。
func (r *Runner) gatePassed(u *user, day int, took time.Duration) {
	if day == 0 || !r.books.Load().pass(u.Region, day, u.ID, took) {
		return
	}
	if r.tt.isGrandPrix(day) {
		r.award(u, day, score.TagGrandPrix)
	} else {
		r.award(u, day, score.TagGatePass)
	}
}

// bannedErr は err が BAN による拒否か。そうなら u を止めて悪評として記録する。
func (r *Runner) bannedErr(u *user, err error) bool {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || apiErr.Code != "ip_banned" {
		return false
	}
	u.banned.Store(true)
	if u.stopped.CompareAndSwap(false, true) {
		r.reputation(u)
	}
	return true
}

func itoa(n int) string { return strconv.Itoa(n) }

// recheckDeletedImage は、削除した投稿の画像を、削除から deletedImageGrace を過ぎたあとで
// もう一度取得する。まだ返れば違反。得点にも失敗にも数えない確認なので、集計しない
// クライアントで送る。
func (r *Runner) recheckDeletedImage(ctx context.Context, u *user, post client.Post) {
	if r.wg == nil {
		return
	}
	// ユーザーはリージョンを移ると API を差し替えるので、送る前に取っておく。
	api := u.API.WithObserver(nil)
	r.wg.Go(func() {
		if sleepUntil(ctx, time.Now().Add(deletedImageGrace+time.Second)) != nil {
			return
		}
		sent := time.Now()
		_, err := api.Image(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), post.ImageURL)
		if err != nil {
			return
		}
		grace := model.Read{Sent: sent, Received: time.Now(), Staleness: deletedImageGrace}
		if r.model.PostLive(post.ID, grace) == model.False {
			r.board.Criticalf("%s: 削除から %s 以上経った投稿 %s の画像が取得できました", u.Username, deletedImageGrace, post.ID)
		}
	})
}
