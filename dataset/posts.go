package dataset

import (
	"math/rand/v2"
	"slices"
	"time"
)

// otherCandidates は、友達以外で RealMoji やコメントを付ける人を探すときに引く候補の数。
const otherCandidates = 24

// HistoryStart は履歴の初日。
func (w *World) HistoryStart() time.Time {
	return w.Config.EndDate.AddDate(0, 0, -(w.Config.Days - 1))
}

// Date は履歴の day 日目（0 始まり）の日付。
func (w *World) Date(day int) time.Time {
	return w.HistoryStart().AddDate(0, 0, day)
}

// DaysIn は month（その月の 1 日）に入る履歴の日（0 始まり）。
func (w *World) DaysIn(month time.Time) []int {
	var days []int
	for day := range w.Config.Days {
		if date := w.Date(day); date.Year() == month.Year() && date.Month() == month.Month() {
			days = append(days, day)
		}
	}
	return days
}

// windowOf は region の day 日目の投稿期間。
func (w *World) windowOf(region string, day int) *Window {
	return w.Windows[day*len(Regions)+slices.Index(Regions, region)]
}

// PostsOn は day 日目の投稿を、ユーザーの順に返す。
func (w *World) PostsOn(day int) []*Post {
	var posts []*Post
	for _, user := range w.Users {
		if post := w.PostOn(user, day); post != nil {
			posts = append(posts, post)
		}
	}
	return posts
}

// NotificationsOn は day 日目の各投稿期間で、そのリージョンの全ユーザーへ送った通知。
func (w *World) NotificationsOn(day int) []*Notification {
	var notifications []*Notification
	for r, region := range Regions {
		window := w.windowOf(region, day)
		for i, user := range w.Users {
			if user.Region == region {
				notifications = append(notifications, &Notification{
					ID: derivedID(w.Config.Seed, "notification", day*len(Regions)+r, i), UserID: user.ID, WindowID: window.ID, CreatedAt: window.StartsAt,
				})
			}
		}
	}
	return notifications
}

// drawPost は user（通し番号）の day 日目の乱数列の最初を引き、投稿したか、それが削除
// されたかを決める。投稿の中身は同じ列の続きから作るので、中身を作らずに投稿の有無がわかる。
func (w *World) drawPost(user, day int) (r *rand.Rand, posted, deleted bool) {
	r = dayRand(w.Config.Seed, "post", day, user)
	if r.Float64() >= w.Users[user].Activity {
		return r, false, false
	}
	return r, true, r.Float64() < 0.01
}

// posted は user（通し番号）が day 日目に削除されていない投稿をしたか。投稿しないと
// その日の他人の投稿は見えない。
func (w *World) posted(user, day int) bool {
	_, posted, deleted := w.drawPost(user, day)
	return posted && !deleted
}

// PostOn は user が day 日目にした投稿を、付いた RealMoji とコメントと合わせて返す。
// 投稿しなかった日は nil。1 人 1 日 1 件まで。
func (w *World) PostOn(user *User, day int) *Post {
	seed := w.Config.Seed
	i := w.userIndex[user.ID]
	r, posted, deleted := w.drawPost(i, day)
	if !posted {
		return nil
	}
	window := w.windowOf(user.Region, day)
	language := languageFor(user.Region, user.ID)
	post := &Post{ID: derivedID(seed, "post", day, i), AuthorID: user.ID, WindowID: window.ID, Caption: choose(r, language.captions)}
	if r.Float64() < 0.8 {
		post.PostedAt = window.StartsAt.Add(time.Duration(5+r.IntN(115)) * time.Second)
	} else {
		post.PostedAt = between(r, window.OnTimeDeadline.Add(time.Second), window.EndedAt)
	}
	post.Visibility = []string{"public", "friends", "friends_of_friends"}[pick(r, []float64{0.2, 0.5, 0.3})]
	if user.AccountType == "certified_public" {
		post.Visibility = "public"
	}
	switch pick(r, []float64{0.7, 0.1, 0.2}) {
	case 0:
		post.Location = Location{Kind: "none"}
	case 1:
		latitude, longitude := r.Float64()*180-90, r.Float64()*360-180
		post.Location = Location{Kind: "exact", Latitude: &latitude, Longitude: &longitude}
	default:
		locality := choose(r, language.localities)
		post.Location = Location{Kind: "approximate", Locality: &locality}
	}
	if deleted {
		deletedAt := post.PostedAt.Add(time.Duration(1+r.IntN(30)) * time.Minute)
		post.DeletedAt = &deletedAt
	}
	post.Image = &Image{ID: derivedID(seed, "post-image", day, i), OwnerID: user.ID, Kind: ImagePost, CreatedAt: post.PostedAt}
	// キャプションの多くは写真のタイトルにする。投稿の乱数列とは別の列で決めるので、投稿の
	// ほかの値は変わらない。
	photo, _ := w.imagePhoto(post.Image)
	if dayRand(seed, "caption", day, i).Float64() < titleCaptionRate {
		post.Caption = photo.titles[language.code]
	}
	if !deleted {
		w.interactions(post, photo, i, day, window)
	}
	return post
}

// titleCaptionRate は投稿のキャプションを写真のタイトルにする割合。残りは椅子の投稿によくある
// 一言（空のこともある）。
const titleCaptionRate = 0.6

// themeCommentRate はコメントを写真のテーマに合わせたものにする割合。残りはどの椅子にも
// 付けられる一言。
const themeCommentRate = 0.6

// interactions は post に RealMoji とコメントを付ける。付けるのはその日に投稿していて
// （投稿しないと他人の投稿は見えない）、ブロック関係になく、公開範囲から見える人だけ。
// 友達を優先する。友達以外は、公開の投稿なら全員から、友達の友達までの投稿なら友達の
// 友達から候補を引く。コメントは公開の投稿か、投稿者の友達だけが付けられる。コメントの多くは
// photo のテーマに合わせる。
func (w *World) interactions(post *Post, photo *chair, author, day int, window *Window) {
	seed := w.Config.Seed
	r := dayRand(seed, "interactions", day, author)
	var friends, others []int
	for _, friend := range w.friendList[author] {
		if w.posted(friend, day) {
			friends = append(friends, friend)
		}
	}
	r.Shuffle(len(friends), func(i, j int) { friends[i], friends[j] = friends[j], friends[i] })
	if post.Visibility != "friends" {
		seen := map[int]bool{}
		for range otherCandidates {
			var candidate int
			if post.Visibility == "public" {
				candidate = r.IntN(len(w.Users))
			} else {
				if len(w.friendList[author]) == 0 {
					break
				}
				friend := w.friendList[author][r.IntN(len(w.friendList[author]))]
				candidate = w.friendList[friend][r.IntN(len(w.friendList[friend]))]
			}
			if candidate == author || seen[candidate] || w.friends[author][candidate] || w.blocked[pairKey(author, candidate)] || !w.posted(candidate, day) {
				continue
			}
			seen[candidate] = true
			others = append(others, candidate)
		}
	}

	popularity := 1.0
	if w.Users[author].AccountType == "certified_public" {
		popularity = 4
	}
	reactors := append(append([]int{}, friends...), others...)
	for _, reactor := range reactors[:min(len(reactors), poisson(r, 2*popularity))] {
		createdAt := between(r, post.PostedAt.Add(time.Minute), window.EndedAt)
		if r.Float64() < 0.1 {
			replacedAt := createdAt
			w.realMoji(post, author, day, reactor, between(r, post.PostedAt, createdAt), &replacedAt, r)
		}
		w.realMoji(post, author, day, reactor, createdAt, nil, r)
	}

	commenters := friends
	if post.Visibility == "public" {
		commenters = reactors
	}
	// コメントの文面は別の列で選び直すので、ほかの値は変わらない。
	texts := dayRand(seed, "comment-text", day, author)
	for _, commenter := range commenters[:min(len(commenters), poisson(r, popularity))] {
		language := languageFor(w.Users[commenter].Region, w.Users[commenter].ID)
		body := choose(r, language.comments)
		if texts.Float64() < themeCommentRate {
			body = choose(texts, themeComments[photo.theme][language.code])
		}
		comment := &Comment{
			ID: derivedID(seed, "comment", day, author, len(post.Comments)), PostID: post.ID, AuthorID: w.Users[commenter].ID,
			Body: body, CreatedAt: between(r, post.PostedAt.Add(time.Minute), window.EndedAt),
		}
		if r.Float64() < 0.03 {
			deletedAt := comment.CreatedAt.Add(time.Minute)
			comment.DeletedAt = &deletedAt
		}
		post.Comments = append(post.Comments, comment)
	}
}

func (w *World) realMoji(post *Post, author, day, reactor int, createdAt time.Time, replacedAt *time.Time, r *rand.Rand) {
	seed, k := w.Config.Seed, len(post.RealMojis)
	reactorID := w.Users[reactor].ID
	post.RealMojis = append(post.RealMojis, &RealMoji{
		ID: derivedID(seed, "realmoji", day, author, k), PostID: post.ID, ReactorID: reactorID, ReactionType: choose(r, reactionTypes),
		Image:     &Image{ID: derivedID(seed, "realmoji-image", day, author, k), OwnerID: reactorID, Kind: ImageRealMoji, CreatedAt: createdAt},
		CreatedAt: createdAt, ReplacedAt: replacedAt,
	})
}
