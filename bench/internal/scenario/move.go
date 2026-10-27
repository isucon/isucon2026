package scenario

import (
	"context"
	"slices"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
)

// リージョンを移るユーザー
//
// 旅をする人は、ある日を過ごし終えたあとで所属するリージョンを変え（PATCH /api/me）、
// 次からは移った先のリージョンから使う。所属リージョンの変更だけはどのリージョンからでも
// 受け付けられ、ほかの API は所属リージョンから使う決まりなので、変えたあとは移った先の
// 国から接続し、通知も移った先で購読し直す。
//
// 移るのは bench が登録した一般のユーザーだけ。初期データのユーザーは initialize で
// 戻せるとは限らず、認定公開アカウントはリージョンの顔なので移らない。グランプリの日と
// その前の日は移らない（グランプリの人数を地域ごとに揃える）。

// moveAttempts は、所属リージョンの変更の応答が届かなかったときに同じ変更を送る回数（最初を含む）。
const moveAttempts = 3

// wandersOff は u が day 日目を過ごし終えたあとで別のリージョンへ移るか。
func (r *Runner) wandersOff(u *user, day int) bool {
	if !u.registered || u.cohort || u.returning || u.certified.Load() || u.leaveOn > 0 || u.stopped.Load() {
		return false
	}
	if day == 0 || day+1 >= r.tt.days() {
		return false
	}
	rate := u.persona.wanderlust
	if r.moveRate > 0 {
		rate = r.moveRate
	}
	return unit(r.seed, "wander", u.Username, itoa(day)) < rate
}

// startListening は通知の購読を始め、止める関数を返す。止める関数は購読のゴルーチンが
// 終わるまで待つ。
func (r *Runner) startListening(ctx context.Context, u *user, out chan notice, subscribed chan<- struct{}) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.listen(ctx, u, out, subscribed)
	}()
	return func() {
		cancel()
		<-done
	}
}

// move は u の所属リージョンを別のリージョンに変える。変える間は通知の購読を止め、変えられたら
// 移った先で、変えられなかったら前のリージョンで購読し直して、その購読を止める関数を返す。
func (r *Runner) move(ctx context.Context, u *user, stop func(), notifications chan notice) func() {
	from := u.Region
	others := slices.DeleteFunc(slices.Clone(geo.Regions), func(region string) bool { return region == from })
	to := others[int(unit(r.seed, "destination", u.Username, from)*float64(len(others)))%len(others)]

	// 変える間は通知を購読しない（変わった瞬間から、前のリージョンでの購読は拒否される）。
	stop()
	// 応答が届かなければ、変わったかどうかわからない。変わっていれば前のリージョンからの要求は
	// すべて 403 になるので、同じ変更を送り直して確かめる（何度送っても結果は同じ）。
	var profile client.Profile
	var err error
	for range moveAttempts {
		w := r.model.BeginMoveUser(u.ID, to)
		profile, err = u.API.UpdateMe(ctx, u.Token, nil, nil, &to)
		settle(w, err)
		if err == nil || isRejection(err) || ctx.Err() != nil {
			break
		}
		r.requestError(ctx, u, "%s: 所属リージョンを %s に変えられません: %v", u.Username, to, err)
	}
	switch {
	case err != nil && isRejection(err):
		// 確実に変わっていないので、前のリージョンのまま続ける。
		r.requestError(ctx, u, "%s: 所属リージョンを %s に変えられません: %v", u.Username, to, err)
		return r.startListening(ctx, u, notifications, nil)
	case err != nil:
		// 送り直しても確かめられなかった（走行が終わった場合を含む）。どちらのリージョンに
		// いるかわからないので、退会の結果がわからない人と同じく、以後は使わず、走行後の
		// 整合性チェックでも確かめない。
		u.stopped.Store(true)
		return func() {}
	case profile.ID != u.ID || profile.Region != to:
		r.invalid(ctx, u, "%s: 所属リージョンを変えた応答が違います（%s）", u.Username, profile.Region)
		return r.startListening(ctx, u, notifications, nil)
	}
	// 移る前のリージョンの通知が残っていれば捨てる。
	select {
	case <-notifications:
	default:
	}
	r.relocate(u, from, to)
	return r.startListening(ctx, u, notifications, nil)
}

// relocate は u を from のユーザーから to のユーザーに移し、接続する国を変える。成長の
// 判定では、移った日の前のリージョンでは「いなくなった人」、移った先では「その日をよそで
// 過ごした人」として、どちらでも通過できなかった人に数えない。
//
// u.Region と u.API は u のゴルーチンだけが書き換え、ほかのゴルーチンは r.mu の中で読む。
func (r *Runner) relocate(u *user, from, to string) {
	b := r.books.Load()
	r.mu.Lock()
	r.users[from] = slices.DeleteFunc(r.users[from], func(c *user) bool { return c == u })
	r.users[to] = append(r.users[to], u)
	u.Region = to
	u.API = u.API.WithCountry(geo.Country(to, int(hashOf(r.seed, "country", u.Username)%1024)))
	r.moves[from+">"+to]++
	r.mu.Unlock()
	if b != nil {
		b.depart(from, u.ID)
		b.arrive(to, u.ID)
	}
	r.cfg.Logger.Info("user moved", "user", u.Username, "from", from, "to", to)
}

// usersByRegion は今の所属リージョンごとのユーザー。ほかのゴルーチンのユーザーの
// リージョンは、u.Region ではなくこれで読む。
func (r *Runner) usersByRegion() map[string][]*user {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]*user, len(r.users))
	for region, list := range r.users {
		out[region] = slices.Clone(list)
	}
	return out
}

// アプリを閉じるユーザー
//
// 一部のユーザーは日を過ごし終えたあとでログアウトし、通知を受けない。次の日が始まって
// しばらくしてから（いつもの投稿の待ちのあと）アプリを開き直してログインし、開いている
// 投稿期間に通知を待たずに投稿する。通知を受けないので、通過までの時間は開き直してから
// 数える。

// signsOff は u が day 日目を過ごし終えたあとでアプリを閉じるか。グランプリの日は閉じない。
func (r *Runner) signsOff(u *user, day int) bool {
	if u.returning || u.certified.Load() || u.leaveOn > 0 || u.stopped.Load() || day == 0 || day >= r.tt.days() {
		return false
	}
	rate := u.persona.signOff
	if r.signOffRate > 0 {
		rate = r.signOffRate
	}
	return unit(r.seed, "signoff", u.Username, itoa(day)) < rate
}

// closeApp は u をログアウトさせ、次の日が始まっていつもの待ちが過ぎるまで休ませてから、
// ログインし直して通知の購読を始め直す。新しい購読を止める関数を返す。ログインし直せ
// なければ、トークンを空にしたまま（次の日は通過できない人として数える）購読を止める。
//
// u.Token は u のゴルーチンだけが書き換え、ほかのゴルーチンは r.mu の中で読む。
func (r *Runner) closeApp(ctx context.Context, u *user, day int, stop func(), notifications chan notice) func() {
	stop()
	select {
	case <-notifications:
	default:
	}
	if err := u.API.Logout(ctx, u.Token); err != nil {
		r.requestError(ctx, u, "%s: ログアウトできません: %v", u.Username, err)
	}
	r.mu.Lock()
	u.Token = ""
	r.mu.Unlock()
	b := r.books.Load()
	if b == nil {
		return func() {}
	}
	wake := b.start.Add(r.tt.start(u.Region, day+1) + r.postDelayOf(u, day+1))
	if sleepUntil(ctx, wake) != nil {
		return func() {}
	}
	fresh := *u.Agent
	if err := fresh.Login(ctx); err != nil {
		if !r.bannedErr(u, err) {
			r.requestError(ctx, u, "%s: ログインし直せません: %v", u.Username, err)
		}
		return func() {}
	}
	r.mu.Lock()
	u.Token = fresh.Token
	r.mu.Unlock()
	return r.startListening(ctx, u, notifications, nil)
}
