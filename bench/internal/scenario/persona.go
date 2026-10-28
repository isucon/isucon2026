package scenario

import (
	"math"
	"math/rand/v2"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/dataset"
)

// persona は利用者ごとの好みと癖。過ごし方（activity）が投稿のあとに何をするかの種類を
// 決めるのに対して、persona はどれだけ・どのようにするかを決める。走行の種とユーザー名から
// 決まるので、同じ走行の同じ人はいつも同じように振る舞う。
type persona struct {
	// appetite は 1 日に閲覧する回数の倍率。
	appetite float64
	// attendance は投稿期間が開いた日に投稿する確率。投稿しない日は何もしない（休む）。
	attendance float64
	// punctuality は通知から投稿までの待ちの癖（0 に近いほどすぐ投稿する）。
	punctuality float64
	// feeds はフィードを選ぶ重み（feedWeights と同じ順）。
	feeds [4]float64
	// scroll はフィードに続きがあるときに次のページも読む確率。
	scroll float64
	// detail、image、profile は、見えた投稿を開く、画像を見る、投稿者のプロフィールを
	// 見る確率。
	detail, image, profile float64
	// reaction はよく使う RealMoji の種類。loyalty の確率でこれを選び、ほかは気分で選ぶ。
	reaction string
	loyalty  float64
	// closeness は親しい人の投稿に目が向く強さ（0 ならどの投稿も同じ）。
	closeness float64
	// friendsOnly は自分の投稿を friends に公開する確率（ほかは friends_of_friends）。
	friendsOnly float64
	// location は投稿に付ける位置情報の癖（none、approximate、exact）。
	location string
	// sociability は友達申請や関係の整理をする頻度の倍率、grooming はプロフィールや
	// フォローを整える頻度の倍率。
	sociability, grooming float64
	// retake は投稿を消したあとに撮り直す確率。
	retake float64
	// wanderlust は 1 日を過ごしたあとで別のリージョンへ移る確率。
	wanderlust float64
	// phrases は口癖のコメント。loyalty の確率でこの中から書く。
	phrases []string
	// avatar と bio は、登録したときにプロフィール画像と自己紹介を設定する確率。
	avatar, bio float64
	// signOff は日を過ごし終えたあとでログアウトし、次の日に開き直してログインする確率。
	signOff float64
	// circle は友達つながりで登録したときに送る友達申請の数。1 割の人は顔が広く、多く送る。
	circle int
	// discover は友達の友達フィードで見た投稿者に友達申請を送る確率（知り合いかもしれない人）。
	// 公開フィードではこの 3 分の 1。
	discover float64
}

// newPersona は username の好みと癖を決める。
func newPersona(seed uint64, username string) persona {
	at := func(field string) float64 { return unit(seed, "persona", field, username) }
	p := persona{
		appetite:    0.45 + 1.1*at("appetite"),
		attendance:  0.85 + 0.15*at("attendance"),
		punctuality: at("punctuality"),
		scroll:      0.2 + 0.6*at("scroll"),
		detail:      0.1 + 0.4*at("detail"),
		image:       0.25 + 0.5*at("image"),
		profile:     0.05 + 0.2*at("profile"),
		loyalty:     0.4 + 0.5*at("loyalty"),
		closeness:   3 * at("closeness"),
		friendsOnly: 0.2 + 0.6*at("visibility"),
		sociability: 0.5 + at("sociability"),
		grooming:    0.5 + at("grooming"),
		retake:      0.3 + 0.6*at("retake"),
	}
	// 好きなフィードは、全体の重みを人ごとに 0.3〜1.7 倍に揺らして決める。
	total := 0.0
	for i, f := range feedWeights {
		p.feeds[i] = f.weight * (0.3 + 1.4*at("feed-"+f.feed))
		total += p.feeds[i]
	}
	for i := range p.feeds {
		p.feeds[i] /= total
	}
	key := hashOf(seed, "persona", "reaction", username)
	habits := rand.New(rand.NewPCG(key, key^0x72656163))
	p.reaction = dataset.ReactionType(habits)
	for range 3 {
		p.phrases = append(p.phrases, dataset.CommentBody(habits))
	}
	p.avatar = 0.3 + 0.5*at("avatar")
	p.bio = 0.2 + 0.5*at("bio")
	// 位置情報は初期データの投稿（なし 7 割、市区町村 2 割、座標 1 割）に近い割合の人が付ける。
	switch x := at("location"); {
	case x < 0.65:
		p.location = "none"
	case x < 0.9:
		p.location = "approximate"
	default:
		p.location = "exact"
	}
	// 15% の人は、ときどき日の終わりにログアウトしてアプリを閉じる。
	if at("signoff") < 0.15 {
		p.signOff = 0.4 + 0.4*at("signoff-rate")
	}
	if at("connector") < 0.1 {
		p.circle = 15 + int(16*at("circle"))
	} else {
		p.circle = 3 + int(8*at("circle"))
	}
	p.discover = 0.05 + 0.2*at("discover")
	// 1 割弱の人はよく旅をし、ほかの人はリージョンを移らない。
	if at("traveler") < 0.08 {
		p.wanderlust = 0.15 + 0.2*at("wanderlust")
	}
	return p
}

// cohortPersona は初期データのユーザーの好みと癖。投稿する日の多さを初期データの活発さに
// 合わせる。
func cohortPersona(seed uint64, username string, active float64) persona {
	p := newPersona(seed, username)
	p.attendance = 0.8 + 0.2*active
	p.wanderlust = 0
	return p
}

// pickFeed は好みに合わせてフィードを選ぶ。
func (u *user) pickFeed() string {
	x := u.rng.Float64()
	for i, f := range feedWeights {
		if x < u.persona.feeds[i] {
			return f.feed
		}
		x -= u.persona.feeds[i]
	}
	return feedWeights[0].feed
}

// pickPost は見えた投稿から 1 つ選ぶ。親しい人（ユーザーの組ごとに決まる）の投稿ほど
// 選ばれやすい。
func (r *Runner) pickPost(u *user, candidates []client.FeedPost) client.FeedPost {
	weights := make([]float64, len(candidates))
	total := 0.0
	for i, post := range candidates {
		weights[i] = math.Exp(u.persona.closeness * unit(r.seed, "closeness", u.Username, post.Author.ID))
		total += weights[i]
	}
	x := u.rng.Float64() * total
	for i, w := range weights {
		if x < w {
			return candidates[i]
		}
		x -= w
	}
	return candidates[len(candidates)-1]
}

// reactionType は送る RealMoji の種類を選ぶ。
func (u *user) reactionType() string {
	if u.rng.Float64() < u.persona.loyalty {
		return u.persona.reaction
	}
	return dataset.ReactionType(u.rng)
}

// commentBody はコメントの本文を選ぶ。
func (u *user) commentBody() string {
	if u.rng.Float64() < u.persona.loyalty {
		return u.persona.phrases[u.rng.IntN(len(u.persona.phrases))]
	}
	return dataset.CommentBody(u.rng)
}

// budgetOf は u のその日の閲覧の回数。
func budgetOf(u *user) int {
	n := int(math.Round(float64(dailyBudget(u.activity)) * u.persona.appetite))
	if u.certified.Load() {
		n = min(n, 10)
	}
	return n
}

// localities はリージョンごとの、投稿に付ける市区町村の候補（初期データと同じ名前）。
var localities = map[string][]string{
	geo.Asia:    {"Shibuya", "Shinjuku", "Umeda"},
	geo.Europe:  {"Kreuzberg", "Montmartre", "Soho"},
	geo.America: {"Brooklyn", "Mission District"},
}

// centers はリージョンごとの、座標を付けるときの中心（緯度、経度）。
var centers = map[string][2]float64{
	geo.Asia:    {35.68, 139.76},
	geo.Europe:  {48.86, 2.35},
	geo.America: {40.71, -74.0},
}

// locationOf は u が day 日目の投稿に付ける位置情報。癖のとおりに付けるが、ときどき気が
// 変わる。座標は小数 4 桁に丸める（どの言語の JSON でも同じ値に戻る桁数）。
func (r *Runner) locationOf(u *user, day int) client.Location {
	kind := u.persona.location
	if x := unit(r.seed, "location", u.Username, itoa(day)); x < 0.15 {
		kind = []string{"none", "approximate", "exact"}[int(x/0.05)]
	}
	switch kind {
	case "approximate":
		names := localities[u.Region]
		name := names[int(unit(r.seed, "locality", u.Username, itoa(day))*float64(len(names)))]
		return client.Location{Kind: "approximate", Locality: &name}
	case "exact":
		center := centers[u.Region]
		round := func(v float64) float64 { return math.Round(v*1e4) / 1e4 }
		latitude := round(center[0] + 0.2*(unit(r.seed, "latitude", u.Username, itoa(day))-0.5))
		longitude := round(center[1] + 0.2*(unit(r.seed, "longitude", u.Username, itoa(day))-0.5))
		return client.Location{Kind: "exact", Latitude: &latitude, Longitude: &longitude}
	default:
		return client.Location{Kind: "none"}
	}
}

// sameLocation は投稿の応答の位置情報が送ったものと同じか。座標は丸めの差を許す。
func sameLocation(sent, got client.Location) bool {
	if sent.Kind != got.Kind {
		return false
	}
	switch sent.Kind {
	case "approximate":
		return got.Locality != nil && *got.Locality == *sent.Locality
	case "exact":
		return got.Latitude != nil && got.Longitude != nil &&
			math.Abs(*got.Latitude-*sent.Latitude) < 1e-6 && math.Abs(*got.Longitude-*sent.Longitude) < 1e-6
	default:
		return true
	}
}

// attends は u が day 日目に投稿するか。初めての日、グランプリ、退会する日、認定公開
// アカウントは必ず投稿する。
func (r *Runner) attends(u *user, day int) bool {
	if day == 0 || r.tt.isGrandPrix(day) || u.leaveOn == day || u.certified.Load() {
		return true
	}
	return unit(r.seed, "attend", u.Username, itoa(day)) < u.persona.attendance
}

// postDelayOf は u が day 日目に通知を受けてから投稿するまでの待ち。日ごとの気分と、
// 人ごとの癖を半分ずつ混ぜる。
func (r *Runner) postDelayOf(u *user, day int) time.Duration {
	mood := unit(r.seed, "delay", u.Username, itoa(day))
	return r.tt.postDelay(min((mood+u.persona.punctuality)/2, 0.999))
}
