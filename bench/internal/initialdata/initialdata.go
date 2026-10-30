// Package initialdata は、アプリが初期データ（dataset）どおりの内容を返すことを確かめる。
//
// 初期データのユーザーとしてログインし、Memories（RealMoji とコメントを含む）、友達の一覧、
// 投稿のエクスポートを dataset から計算した期待値と比べる。画像は署名付き URL が持ち主の
// 鍵で署名されていることと、中身が一致することも確かめる。履歴は読むだけで書き込まない。
package initialdata

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/rand/v2"
	neturl "net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/dataset"
)

// アプリが 1 ページ、または投稿 1 件に付けて返す最大件数。
const pageSize = 50

// imageConcurrency は 1 人のユーザーが同時に取得する画像の数。ブラウザが 1 つのホストに
// 張る接続数と同じくらいにする。
const imageConcurrency = 6

// エクスポートの ZIP を展開するときの上限（バイト）。小さな ZIP から大きな項目を展開させて
// bench の時間とメモリを使わせないようにする。値は Large の初期データの実際の大きさに十分な
// 余裕を持たせている。
const (
	// maxExportJSONBytes は posts.json を展開した大きさの上限。Large で最も投稿の多いユーザー
	// （363 件）でも約 300 KB（ASCII 以外を \u でエスケープしても約 330 KB）。
	maxExportJSONBytes = 16 << 20
	// maxExportImageBytes は画像の項目 1 つを展開した大きさの上限。初期データの画像は 320 px の
	// JPEG（Large の投稿で平均約 15 KB、画素をそのまま並べても 300 KB）で、Router が書き換えた
	// 項目はダイジェスト（数十バイト）。
	maxExportImageBytes = 4 << 20
	// maxExportExpandedBytes は 1 回のエクスポートの検証で展開する量の合計の上限。応答の ZIP の
	// 上限（client の 256 MiB）と同じ。
	maxExportExpandedBytes = 256 << 20
)

// Checker は関係の初期データの索引を持つ。投稿は履歴全体を持たず、確かめるユーザーの
// 確かめる日の分だけ dataset から作り直す。
type Checker struct {
	world *dataset.World
	// MemoryMonths は Check が Memories を読む月の数。履歴のある月から無作為に選ぶ。
	// 0 ならすべての月を読む。
	MemoryMonths int
	// ImageFetches は Check が中身を取得して比べる画像の数の上限。0 ならすべて取得する。
	// 署名はすべて確かめる。海外のサーバーは DB まで遠く、RealMoji の多いユーザーの画像を
	// すべて取ると数分かかるので、無作為に選んだものだけを取る。
	ImageFetches int
	// ImageDigests は初期データの画像の SHA-256 の表（なければ nil）。あれば Router に画像の
	// SHA-256 を返させてこの表と比べ、画像を描き直さない。
	ImageDigests *dataset.ImageDigests

	// exportJSONLimit、exportImageLimit、exportBudget はエクスポートの ZIP を展開するときの
	// 上限（maxExportJSONBytes などの値。テストで小さくする）。
	exportJSONLimit, exportImageLimit, exportBudget int64

	users             map[string]*dataset.User
	windows           map[string]*dataset.Window
	friendshipsByUser map[string][]*dataset.Friendship
}

// New は world から期待値を引くための索引を作る。退会したユーザーとの友達関係はアプリが
// 返さないので、ここで除いておく。
func New(world *dataset.World) *Checker {
	c := &Checker{
		world:             world,
		MemoryMonths:      2,
		ImageFetches:      16,
		exportJSONLimit:   maxExportJSONBytes,
		exportImageLimit:  maxExportImageBytes,
		exportBudget:      maxExportExpandedBytes,
		users:             map[string]*dataset.User{},
		windows:           map[string]*dataset.Window{},
		friendshipsByUser: map[string][]*dataset.Friendship{},
	}
	for _, u := range world.Users {
		c.users[u.ID] = u
	}
	for _, w := range world.Windows {
		c.windows[w.ID] = w
	}
	for _, f := range world.Friendships {
		if c.active(f.UserLowID) && c.active(f.UserHighID) {
			c.friendshipsByUser[f.UserLowID] = append(c.friendshipsByUser[f.UserLowID], f)
			c.friendshipsByUser[f.UserHighID] = append(c.friendshipsByUser[f.UserHighID], f)
		}
	}
	return c
}

func (c *Checker) active(userID string) bool {
	return c.users[userID].Status == "active"
}

// posts は user が days の各日にした投稿のうち、アプリが返すもの（削除されていないもの）。
func (c *Checker) posts(user *dataset.User, days []int) []*dataset.Post {
	var posts []*dataset.Post
	for _, day := range days {
		if p := c.world.PostOn(user, day); p != nil && p.DeletedAt == nil {
			posts = append(posts, p)
		}
	}
	return posts
}

// allDays は履歴のすべての日。
func (c *Checker) allDays() []int {
	days := make([]int, c.world.Config.Days)
	for day := range days {
		days[day] = day
	}
	return days
}

// Targets は検証するユーザーを選ぶ。友達の最も多い通常ユーザー（友達の一覧にページ送りが
// 要る）と、各リージョンから無作為に選んだ通常ユーザー、無作為に選んだ認定公開アカウント
// （RealMoji とコメントが多い）。対象を走行ごとに変えるので、決まったユーザーだけを正しく返す
// 実装では通らない。選び方は走行の種 seed で決まり、同じ種なら同じユーザーを選ぶ。
func (c *Checker) Targets(seed uint64) []*dataset.User {
	r := rand.New(rand.NewPCG(seed, targetsStream))
	var hub *dataset.User
	var certified []*dataset.User
	regular := map[string][]*dataset.User{}
	for _, u := range c.world.Users {
		if u.Status != "active" || u.Role != dataset.RoleHistory {
			continue
		}
		if u.AccountType == "certified_public" {
			certified = append(certified, u)
			continue
		}
		regular[u.Region] = append(regular[u.Region], u)
		if hub == nil || len(c.friendshipsByUser[u.ID]) > len(c.friendshipsByUser[hub.ID]) {
			hub = u
		}
	}
	targets := []*dataset.User{hub}
	for _, region := range dataset.Regions {
		if users := regular[region]; len(users) > 0 {
			if u := users[r.IntN(len(users))]; u != hub {
				targets = append(targets, u)
			}
		}
	}
	return append(targets, certified[r.IntN(len(certified))])
}

// targetsStream は Targets の乱数列を、走行のほかの乱数列と分ける。
const targetsStream = 0x74617267657473

// userStream は Check で userID のユーザーを確かめるときの乱数列を、ほかのユーザーの乱数列と
// 分ける。並行に確かめても、ユーザーごとの抽選は確かめる順に左右されない。
func userStream(userID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(userID))
	return h.Sum64()
}

// globalRand は math/rand/v2 の既定の乱数（並行に使ってよい）。負荷走行の確認は種を決めずに
// これで抽選する。
var globalRand = rand.New(globalSource{})

type globalSource struct{}

func (globalSource) Uint64() uint64 { return rand.Uint64() }

// HistoryUsers は region の、負荷走行で書き込まない有効な初期データのユーザー。負荷走行では
// 思い出を見返すユーザーとしてログインし、履歴を読むだけにする。
func (c *Checker) HistoryUsers(region string) []*dataset.User {
	var users []*dataset.User
	for _, u := range c.world.Users {
		if u.Region == region && u.Status == "active" && u.Role == dataset.RoleHistory {
			users = append(users, u)
		}
	}
	return users
}

// CohortUsers は region の、負荷走行で書き込んでよい初期データのユーザー（有効な cohort）。
func (c *Checker) CohortUsers(region string) []*dataset.User {
	var users []*dataset.User
	for _, u := range c.world.Users {
		if u.Region == region && u.Status == "active" && u.Role == dataset.RoleCohort {
			users = append(users, u)
		}
	}
	return users
}

// Report は Check で確かめた内容の内訳。運営向けのログに出し、遅い段階を見分けるのに使う。
type Report struct {
	// Months は Memories を読む月（YYYY-MM）。
	Months []string
	// Steps は段階（login、memories、friends、export、images）ごとの所要時間と件数。失敗した
	// 段階までを入れる。
	Steps []Step
}

// Step は Check の 1 段階の所要時間と、確かめた件数（Memories とエクスポートは投稿、友達の
// 一覧は友達、images は中身を取得した画像）。
type Step struct {
	Name    string
	Elapsed time.Duration
	Count   int
}

// Check は user としてログインし、そのユーザーの初期データをすべて確かめる。api は国を
// 持たないクライアントでよく、ユーザーのリージョンの国はここで付ける。Memories の月と取得する
// 画像は走行の種 seed とユーザーで決まり、同じ種なら同じものを確かめる。
func (c *Checker) Check(ctx context.Context, api *client.Client, user *dataset.User, seed uint64) (Report, error) {
	a := agent.New(api, user.Region, 0)
	a.ID, a.Username, a.SecretKey = user.ID, user.Username, user.SecretKey
	u := &userCheck{Checker: c, a: a, rng: rand.New(rand.NewPCG(seed, userStream(user.ID)))}
	var report Report
	step := func(name string, check func(context.Context) (int, error)) error {
		start := time.Now()
		count, err := check(ctx)
		report.Steps = append(report.Steps, Step{Name: name, Elapsed: time.Since(start), Count: count})
		return err
	}
	if err := step("login", func(ctx context.Context) (int, error) { return 1, a.Login(ctx) }); err != nil {
		return report, fmt.Errorf("初期データのユーザー %s としてログインできません: %w", user.Username, err)
	}
	months := u.drawMonths()
	for _, month := range months {
		report.Months = append(report.Months, month.Format("2006-01"))
	}
	for _, s := range []struct {
		name  string
		check func(context.Context) (int, error)
	}{
		{"memories", func(ctx context.Context) (int, error) { return u.checkMemories(ctx, months) }},
		{"friends", u.checkFriends},
		{"export", u.checkExport},
		{"images", func(ctx context.Context) (int, error) {
			u.sampleImages(c.ImageFetches)
			return len(u.images), u.fetchImages(ctx)
		}},
	} {
		if err := step(s.name, s.check); err != nil {
			return report, fmt.Errorf("初期データのユーザー %s: %w", user.Username, err)
		}
	}
	return report, nil
}

// sampleImages は取得を予約した画像から n 枚を無作為に残す（n が 0 ならすべて残す）。
func (u *userCheck) sampleImages(n int) {
	if n > 0 && len(u.images) > n {
		u.random().Shuffle(len(u.images), func(i, j int) { u.images[i], u.images[j] = u.images[j], u.images[i] })
		u.images = u.images[:n]
	}
}

// CheckMemoriesDate は a の date の Memories を読み、初期データと比べる。画像は images 枚を
// 無作為に選んで取得する（thorough なら中身も比べる）。
func (c *Checker) CheckMemoriesDate(ctx context.Context, a *agent.Agent, day int, images int, thorough bool) error {
	u := &userCheck{Checker: c, a: a, sampleSignatures: true, skipContents: !thorough}
	want := c.posts(c.users[a.ID], []int{day})
	date := c.world.Date(day).Format(time.DateOnly)
	var got []client.MemoryPost
	var before *client.Post
	for {
		page, err := a.API.MemoriesDate(ctx, a.Token, date, before)
		if err != nil {
			return err
		}
		got = append(got, page.Items...)
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1].Post
	}
	if len(got) != len(want) {
		return fmt.Errorf("Memories（%s）の投稿が %d 件です（期待値 %d 件）", date, len(got), len(want))
	}
	for i, item := range got {
		if err := u.checkMemoryPost(item, want[i]); err != nil {
			return fmt.Errorf("Memories（%s）: %w", date, err)
		}
	}
	if thorough {
		if err := u.verifySampled(); err != nil {
			return err
		}
	}
	u.sampleImages(images)
	return u.fetchImages(ctx)
}

// CheckExport は a のエクスポートを取り、投稿の一覧（posts.json）を初期データと比べる。
// 画像は images 枚を無作為に選んで中身を比べる（AllImages ならすべて、0 なら比べない）。isNew が true を返す投稿
// （負荷走行で作られた投稿）は比べない。
func (c *Checker) CheckExport(ctx context.Context, a *agent.Agent, images int, isNew func(id string) bool) error {
	u := &userCheck{Checker: c, a: a}
	_, err := u.exportPosts(ctx, images, isNew)
	return err
}

// SpotCheck は a の履歴から無作為に n 枚の投稿の画像を選び、署名付き URL を bench が作って
// 直接取得し、中身を初期データと比べる。古い画像を消すか差し替える実装を見つける。
func (c *Checker) SpotCheck(ctx context.Context, a *agent.Agent, n int) error {
	user := c.users[a.ID]
	posts := c.posts(user, c.allDays())
	if len(posts) == 0 {
		return nil
	}
	exp := time.Now().Add(time.Hour).Unix()
	for range n {
		p := posts[rand.IntN(len(posts))]
		url := "/api/images/" + p.Image.ID + "?" + neturl.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {imageurl.Sign(user.SecretKey, p.Image.ID, exp)}}.Encode()
		data, err := c.image(ctx, a.API, url)
		if err != nil {
			return err
		}
		if !c.sameImage(data, p.Image) {
			return fmt.Errorf("投稿 %s の画像 %s が初期データと違います", p.ID, p.Image.ID)
		}
	}
	return nil
}

// image は初期データの画像を取得する。SHA-256 の表があれば Router に SHA-256 を返させる。
func (c *Checker) image(ctx context.Context, api *client.Client, url string) ([]byte, error) {
	if c.ImageDigests != nil {
		return api.InitialImage(ctx, url)
	}
	return api.Image(ctx, url)
}

// sameImage は got（画像のバイト列か Router が返したダイジェスト）が初期データの画像 img と
// 同じかを返す。SHA-256 の表に img があれば、画像を描き直さずに比べる。
func (c *Checker) sameImage(got []byte, img *dataset.Image) bool {
	if c.ImageDigests != nil {
		if want, ok := c.ImageDigests.Lookup(img.ID); ok {
			if sum, ok := client.ImageSHA256(got); ok {
				return [dataset.ImageDigestSize]byte(sum[:dataset.ImageDigestSize]) == want
			}
		}
	}
	return client.SameImage(got, c.world.ImageBytes(img))
}

// userCheck は 1 人のユーザーの検証。画像は URL の署名をその場で確かめ、中身の取得は
// 最後にまとめて並行に行う。sampleSignatures なら署名は確かめずに控えておき、最後に
// 無作為に選んだものだけを確かめる。
type userCheck struct {
	*Checker
	a                *agent.Agent
	images           []imageFetch
	sampleSignatures bool
	signatures       []signatureCheck
	// skipContents なら画像の中身を取得するだけで、初期データと比べない。
	skipContents bool
	// rng は抽選（月、署名、画像）に使う乱数。nil なら globalRand を使う。
	rng *rand.Rand
}

// random は u の抽選に使う乱数。
func (u *userCheck) random() *rand.Rand {
	if u.rng != nil {
		return u.rng
	}
	return globalRand
}

// sampledSignatures は、負荷走行で履歴を 1 か月読むたびに確かめる署名の数。署名（HMAC を
// 1000 回）は重く、1 か月分の投稿、RealMoji、プロフィールの画像をすべて確かめると bench の
// CPU の大半を使う。署名の方式の誤りは数件で見つかり、ときどき誤る実装も走行中に何度も引く。
const sampledSignatures = 8

type signatureCheck struct {
	url, imageID, secretKey string
}

// verify は url が imageID の画像を指し、secretKey の持ち主が署名したことを確かめる
// （sampleSignatures なら控えておく）。
func (u *userCheck) verify(url, imageID, secretKey string) error {
	if u.sampleSignatures {
		u.signatures = append(u.signatures, signatureCheck{url: url, imageID: imageID, secretKey: secretKey})
		return nil
	}
	return imageurl.Verify(url, imageID, secretKey, time.Now())
}

// verifySampled は控えた署名から sampledSignatures 件を無作為に選んで確かめる。
func (u *userCheck) verifySampled() error {
	u.random().Shuffle(len(u.signatures), func(i, j int) { u.signatures[i], u.signatures[j] = u.signatures[j], u.signatures[i] })
	for _, sig := range u.signatures[:min(len(u.signatures), sampledSignatures)] {
		if err := imageurl.Verify(sig.url, sig.imageID, sig.secretKey, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

type imageFetch struct {
	url   string
	image *dataset.Image
}

// checkImage は url が img の持ち主の署名した img を指すことを確かめ、中身の取得を予約する。
func (u *userCheck) checkImage(url string, img *dataset.Image) error {
	if err := u.verify(url, img.ID, u.users[img.OwnerID].SecretKey); err != nil {
		return err
	}
	u.images = append(u.images, imageFetch{url: url, image: img})
	return nil
}

// fetchImages は予約した画像を並行に取得し、中身が初期データと一致することを確かめる。
func (u *userCheck) fetchImages(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	slots := make(chan struct{}, imageConcurrency)
	var wg sync.WaitGroup
	for _, fetch := range u.images {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			var err error
			if u.skipContents {
				// 比べないので、Router に SHA-256（XXH3 より重い）を求めない。
				_, err = u.a.API.Image(ctx, fetch.url)
			} else if data, e := u.image(ctx, u.a.API, fetch.url); e != nil {
				err = e
			} else if !u.sameImage(data, fetch.image) {
				err = fmt.Errorf("画像 %s の中身が初期データと違います", fetch.image.ID)
			}
			if err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	return nil
}

// checkUser は埋め込まれたユーザーを確かめる。bio は見せるかどうかが場面で変わるので、
// withBio のときだけ比べる。プロフィール画像は URL の署名だけを確かめる。
func (u *userCheck) checkUser(got client.UserSummary, want *dataset.User, withBio bool) error {
	if got.ID != want.ID || got.Username != want.Username || got.DisplayName != want.DisplayName || got.AccountType != want.AccountType {
		return fmt.Errorf("ユーザー %s の情報が初期データと違います", want.Username)
	}
	if withBio && got.Bio != want.Bio {
		return fmt.Errorf("ユーザー %s の自己紹介が初期データと違います", want.Username)
	}
	switch {
	case want.ProfileImage == nil && got.ProfileImageURL != nil:
		return fmt.Errorf("ユーザー %s にないはずのプロフィール画像があります", want.Username)
	case want.ProfileImage != nil && got.ProfileImageURL == nil:
		return fmt.Errorf("ユーザー %s のプロフィール画像がありません", want.Username)
	case want.ProfileImage != nil:
		return u.verify(*got.ProfileImageURL, want.ProfileImage.ID, want.SecretKey)
	}
	return nil
}

func (u *userCheck) checkPost(got client.Post, want *dataset.Post) error {
	c := u.Checker
	window := c.windows[want.WindowID]
	onTime := !want.PostedAt.After(window.OnTimeDeadline)
	var lateSeconds int64
	if !onTime {
		lateSeconds = int64(want.PostedAt.Sub(window.OnTimeDeadline) / time.Second)
	}
	switch {
	case got.ID != want.ID || got.PostingWindowID != want.WindowID:
		return fmt.Errorf("投稿 %s の ID か投稿期間が違います", want.ID)
	case got.Caption != want.Caption || got.Visibility != want.Visibility:
		return fmt.Errorf("投稿 %s のキャプションか公開範囲が初期データと違います", want.ID)
	case !got.PostedAt.Equal(want.PostedAt) || got.OnTime != onTime || got.LateSeconds != lateSeconds:
		return fmt.Errorf("投稿 %s の投稿時刻か遅延の情報が初期データと違います", want.ID)
	case !sameLocation(got.Location, want.Location):
		return fmt.Errorf("投稿 %s の位置情報が初期データと違います", want.ID)
	}
	return u.checkUser(got.Author, c.users[want.AuthorID], true)
}

func sameLocation(got client.Location, want dataset.Location) bool {
	sameFloat := func(a, b *float64) bool {
		return (a == nil) == (b == nil) && (a == nil || math.Abs(*a-*b) < 1e-9)
	}
	sameString := func(a, b *string) bool {
		return (a == nil) == (b == nil) && (a == nil || *a == *b)
	}
	return got.Kind == want.Kind && sameFloat(got.Latitude, want.Latitude) && sameFloat(got.Longitude, want.Longitude) && sameString(got.Locality, want.Locality)
}

// newestFirst は (時刻, ID) の降順に並べる比較関数を作る。
func newestFirst[T any](at func(T) time.Time, id func(T) string) func(a, b T) int {
	return func(a, b T) int {
		return cmp.Or(at(b).Compare(at(a)), cmp.Compare(id(b), id(a)))
	}
}

// drawMonths は履歴のある月から、Memories を読む MemoryMonths か月を選ぶ（0 ならすべて）。
func (u *userCheck) drawMonths() []time.Time {
	months := u.HistoryMonths()
	if u.MemoryMonths > 0 {
		u.random().Shuffle(len(months), func(i, j int) { months[i], months[j] = months[j], months[i] })
		months = months[:min(len(months), u.MemoryMonths)]
	}
	return months
}

// checkMemories は months の Memories を最後のページまで読んで、投稿、RealMoji、コメントを
// 比べる。比べた投稿の数を返す。
func (u *userCheck) checkMemories(ctx context.Context, months []time.Time) (int, error) {
	count := 0
	for _, month := range months {
		n, err := u.checkMonth(ctx, month, func(client.Post) bool { return false })
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// HistoryMonths は初期データの履歴がある月（各月の 1 日）。
func (c *Checker) HistoryMonths() []time.Time {
	cfg := c.world.Config
	first := cfg.EndDate.AddDate(0, 0, -(cfg.Days - 1))
	var months []time.Time
	for month := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC); !month.After(cfg.EndDate); month = month.AddDate(0, 1, 0) {
		months = append(months, month)
	}
	return months
}

// CheckMemories は a の month の Memories を最後のページまで読み、初期データと比べる。images が
// 0 なら画像の署名をすべて確かめ、中身もすべて取得して比べる。images が正なら、署名は無作為に
// 選んだ sampledSignatures 件、中身は無作為に選んだ images 枚だけを確かめる（負荷走行用）。
// 負荷走行で作られた投稿（isNew が true）は比べない。
//
// thorough でなければ、画像の署名と中身（期待する JPEG を作るのが重い）は確かめず、中身の
// 取得だけを行う。Memories の投稿、RealMoji、コメントは常に比べる。
func (c *Checker) CheckMemories(ctx context.Context, a *agent.Agent, month time.Time, isNew func(client.Post) bool, images int, thorough bool) error {
	u := &userCheck{Checker: c, a: a, sampleSignatures: images > 0 || !thorough, skipContents: !thorough}
	if _, err := u.checkMonth(ctx, month, isNew); err != nil {
		return err
	}
	if thorough {
		if err := u.verifySampled(); err != nil {
			return err
		}
	}
	if images > 0 && len(u.images) > images {
		u.random().Shuffle(len(u.images), func(i, j int) { u.images[i], u.images[j] = u.images[j], u.images[i] })
		u.images = u.images[:images]
	}
	return u.fetchImages(ctx)
}

// checkMonth は month の Memories を最後のページまで読み、初期データの投稿、RealMoji、
// コメントと比べる。isNew が true を返す投稿は除いてから比べる。比べた投稿の数を返す。
func (u *userCheck) checkMonth(ctx context.Context, month time.Time, isNew func(client.Post) bool) (int, error) {
	c, a := u.Checker, u.a
	want := c.posts(c.users[a.ID], c.world.DaysIn(month))
	slices.SortFunc(want, newestFirst(func(p *dataset.Post) time.Time { return p.PostedAt }, func(p *dataset.Post) string { return p.ID }))

	var got []client.MemoryPost
	var before *client.Post
	for {
		page, err := a.API.MemoriesMonth(ctx, a.Token, month.Format("2006-01"), before)
		if err != nil {
			return len(got), err
		}
		for _, item := range page.Items {
			if !isNew(item.Post) {
				got = append(got, item)
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1].Post
	}
	if len(got) != len(want) {
		return len(got), fmt.Errorf("Memories（%s）の投稿が %d 件です（期待値 %d 件）", month.Format("2006-01"), len(got), len(want))
	}
	for i, item := range got {
		if err := u.checkMemoryPost(item, want[i]); err != nil {
			return i, fmt.Errorf("Memories（%s）: %w", month.Format("2006-01"), err)
		}
	}
	return len(got), nil
}

func (u *userCheck) checkMemoryPost(got client.MemoryPost, want *dataset.Post) error {
	c := u.Checker
	if err := u.checkPost(got.Post, want); err != nil {
		return err
	}
	if err := u.checkImage(got.ImageURL, want.Image); err != nil {
		return err
	}

	var reactions []*dataset.RealMoji
	for _, r := range want.RealMojis {
		if r.ReplacedAt == nil && c.active(r.ReactorID) {
			reactions = append(reactions, r)
		}
	}
	slices.SortFunc(reactions, newestFirst(func(r *dataset.RealMoji) time.Time { return r.CreatedAt }, func(r *dataset.RealMoji) string { return r.ID }))
	reactions = reactions[:min(len(reactions), pageSize)]
	if len(got.RealMojis) != len(reactions) {
		return fmt.Errorf("投稿 %s の RealMoji が %d 件です（期待値 %d 件）", want.ID, len(got.RealMojis), len(reactions))
	}
	for i, r := range got.RealMojis {
		w := reactions[i]
		if r.ID != w.ID || r.ReactionType != w.ReactionType || !r.CreatedAt.Equal(w.CreatedAt) {
			return fmt.Errorf("投稿 %s の %d 件目の RealMoji が初期データと違います", want.ID, i+1)
		}
		if err := u.checkUser(r.Reactor, c.users[w.ReactorID], false); err != nil {
			return fmt.Errorf("投稿 %s の RealMoji %s: %w", want.ID, w.ID, err)
		}
		if err := u.checkImage(r.ImageURL, w.Image); err != nil {
			return fmt.Errorf("投稿 %s の RealMoji %s: %w", want.ID, w.ID, err)
		}
	}

	var comments []*dataset.Comment
	for _, cm := range want.Comments {
		if cm.DeletedAt == nil && c.active(cm.AuthorID) {
			comments = append(comments, cm)
		}
	}
	slices.SortFunc(comments, func(x, y *dataset.Comment) int {
		return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), cmp.Compare(x.ID, y.ID))
	})
	comments = comments[:min(len(comments), pageSize)]
	if len(got.Comments) != len(comments) {
		return fmt.Errorf("投稿 %s のコメントが %d 件です（期待値 %d 件）", want.ID, len(got.Comments), len(comments))
	}
	for i, cm := range got.Comments {
		w := comments[i]
		if cm.ID != w.ID || cm.Body != w.Body || !cm.CreatedAt.Equal(w.CreatedAt) {
			return fmt.Errorf("投稿 %s の %d 件目のコメントが初期データと違います", want.ID, i+1)
		}
		if err := u.checkUser(cm.Author, c.users[w.AuthorID], false); err != nil {
			return fmt.Errorf("投稿 %s のコメント %s: %w", want.ID, w.ID, err)
		}
	}
	return nil
}

// checkFriends は友達の一覧を最後のページまで読み、友達関係の新しい順に並ぶことを確かめる。
// 読んだ友達の数を返す。
func (u *userCheck) checkFriends(ctx context.Context) (int, error) {
	c, a := u.Checker, u.a
	want := slices.Clone(c.friendshipsByUser[a.ID])
	slices.SortFunc(want, newestFirst(func(f *dataset.Friendship) time.Time { return f.CreatedAt }, func(f *dataset.Friendship) string { return f.ID }))

	var got []client.Relationship
	var before *client.Relationship
	for {
		page, err := a.API.Relationships(ctx, a.Token, "friends", before)
		if err != nil {
			return len(got), err
		}
		got = append(got, page.Items...)
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = &page.Items[len(page.Items)-1]
	}
	if len(got) != len(want) {
		return len(got), fmt.Errorf("友達の一覧が %d 件です（期待値 %d 件）", len(got), len(want))
	}
	for i, r := range got {
		f := want[i]
		friendID := f.UserLowID
		if friendID == a.ID {
			friendID = f.UserHighID
		}
		if r.RelationID != f.ID || r.Status != "friends" || !r.UpdatedAt.Equal(f.CreatedAt) {
			return len(got), fmt.Errorf("友達の一覧の %d 件目が初期データと違います", i+1)
		}
		if err := u.checkUser(r.User, c.users[friendID], true); err != nil {
			return len(got), fmt.Errorf("友達の一覧: %w", err)
		}
	}
	return len(got), nil
}

type exportedPost struct {
	client.Post
	ImagePath string `json:"imagePath"`
}

// AllImages は、エクスポートのすべての画像を比べることを表す。
const AllImages = -1

// checkExport は ZIP の posts.json が全投稿を古い順に並べ、各画像が中身どおりであることを
// 確かめる。エクスポートの投稿の数を返す。
func (u *userCheck) checkExport(ctx context.Context) (int, error) {
	return u.exportPosts(ctx, AllImages, func(string) bool { return false })
}

// exportPosts はエクスポートの posts.json を初期データと比べ、画像は images 枚（AllImages なら
// すべて、0 なら比べない）を比べる。期待する画像は描き直して作るので重い。isNew が true を
// 返す投稿は除いてから比べる。比べた投稿の数を返す。
func (u *userCheck) exportPosts(ctx context.Context, images int, isNew func(id string) bool) (int, error) {
	c, a := u.Checker, u.a
	export, err := a.API.Export(ctx, a.Token)
	if err != nil {
		return 0, err
	}
	archive, err := zip.NewReader(bytes.NewReader(export.Data), int64(len(export.Data)))
	if err != nil {
		return 0, fmt.Errorf("エクスポートの ZIP を開けません: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range archive.File {
		files[f.Name] = f
	}
	expansion := &zipBudget{total: c.exportBudget, remaining: c.exportBudget}
	var all []exportedPost
	if err := expansion.decodeJSON(ctx, files["posts.json"], c.exportJSONLimit, &all); err != nil {
		return 0, fmt.Errorf("エクスポートの posts.json を読めません: %w", err)
	}
	var got []exportedPost
	for _, p := range all {
		if !isNew(p.ID) {
			got = append(got, p)
		}
	}
	want := c.posts(c.users[a.ID], c.allDays())
	slices.SortFunc(want, func(x, y *dataset.Post) int { return cmp.Or(x.PostedAt.Compare(y.PostedAt), cmp.Compare(x.ID, y.ID)) })
	if len(got) != len(want) {
		return len(got), fmt.Errorf("エクスポートの投稿が %d 件です（期待値 %d 件）", len(got), len(want))
	}
	check := make([]bool, len(got))
	switch {
	case images == 0:
	case images < 0 || images >= len(got):
		for i := range check {
			check[i] = true
		}
	default:
		for _, i := range u.random().Perm(len(got))[:images] {
			check[i] = true
		}
	}
	for i, p := range got {
		// 画像の比較（描き直すと 1 枚数ミリ秒）が続くので、期限が過ぎたらやめる。
		if err := ctx.Err(); err != nil {
			return i, err
		}
		if err := u.checkPost(p.Post, want[i]); err != nil {
			return i, fmt.Errorf("エクスポート: %w", err)
		}
		if !check[i] {
			continue
		}
		if err := imageurl.Verify(p.ImageURL, want[i].Image.ID, a.SecretKey, time.Now()); err != nil {
			return i, fmt.Errorf("エクスポート: %w", err)
		}
		image, err := expansion.read(ctx, files[p.ImagePath], c.exportImageLimit)
		if err != nil {
			return i, fmt.Errorf("エクスポートの %s を読めません: %w", p.ImagePath, err)
		}
		if !export.Trusted(image) || !c.sameImage(image, want[i].Image) {
			return i, fmt.Errorf("エクスポートの %s が投稿 %s の画像と違います", p.ImagePath, want[i].ID)
		}
	}
	return len(got), nil
}

// zipBudget は 1 回のエクスポートの検証で ZIP の項目を展開できる量（バイト）の合計と残り。
type zipBudget struct {
	total, remaining int64
}

// read は ZIP の項目 f を展開して読む。展開した大きさが limit か残りの量を超えれば、全部を
// 展開する前にやめてエラーにする（項目の見出しの大きさで超えるものは展開しない）。ctx が
// 終われば読むのをやめて ctx のエラーを返す。
func (b *zipBudget) read(ctx context.Context, f *zip.File, limit int64) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("ファイルがありません")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := min(limit, b.remaining)
	tooLarge := func() error {
		if n < limit {
			return fmt.Errorf("エクスポートを展開した量が合わせて %d バイトを超えます", b.total)
		}
		return fmt.Errorf("展開すると %d バイトを超えます", limit)
	}
	// 見出しの大きさと実際に展開した大きさが違えば、archive/zip が読み取りのエラーにする。
	if f.UncompressedSize64 > uint64(n) {
		return nil, tooLarge()
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(ctxReader{ctx: ctx, r: r}, n+1))
	b.remaining -= int64(len(data))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > n {
		return nil, tooLarge()
	}
	return data, nil
}

// decodeJSON は ZIP の項目 f を read で読み、JSON として out に読む。
func (b *zipBudget) decodeJSON(ctx context.Context, f *zip.File, limit int64, out any) error {
	data, err := b.read(ctx, f, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// ctxReader は ctx が終わったら読み取りをやめる io.Reader。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
