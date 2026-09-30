package dataset

import (
	"math/rand/v2"
	"slices"
	"time"
)

// 認定公開アカウントは先頭のユーザーで、リージョンはこの順に割り当てる。サービスはアジアで
// 広まっていて、海外にはまだ少ない。
var certifiedRegions = []string{Asia, Asia, Asia, Asia, Asia, Asia, Asia, Asia, Europe, America}

// regionWeights は通常のユーザーのリージョンの比率（asia、europe、america）。
var regionWeights = []float64{0.99, 0.005, 0.005}

// 投稿期間の開始時刻の基準（UTC）。リージョンごとに現地の昼ごろにする。
var windowBaseHour = map[string]int{Asia: 3, Europe: 11, America: 18}

type generator struct {
	cfg   Config
	world *World
	ids   idGenerator
	r     *rand.Rand

	historyStart time.Time
	friends      []map[int]bool
	blocked      map[[2]int]bool
}

// Generate は cfg から関係の初期データを作る。関係（友達、フォロー、ブロック）はすべて
// 履歴の開始前にできあがっているものとし、履歴の期間中は変わらない。
func Generate(cfg Config) *World {
	g := &generator{
		cfg:          cfg,
		world:        &World{Config: cfg},
		ids:          idGenerator{newRand(cfg.Seed, "id")},
		historyStart: cfg.EndDate.AddDate(0, 0, -(cfg.Days - 1)),
		blocked:      map[[2]int]bool{},
	}
	g.r = newRand(cfg.Seed, "users")
	g.users()
	g.r = newRand(cfg.Seed, "friendships")
	g.friendships()
	g.r = newRand(cfg.Seed, "blocks")
	g.blocks()
	g.r = newRand(cfg.Seed, "friend-requests")
	g.openRequests()
	g.r = newRand(cfg.Seed, "follows")
	g.follows()
	g.r = newRand(cfg.Seed, "windows")
	g.windows()

	w := g.world
	w.userIndex = make(map[string]int, len(w.Users))
	for i, user := range w.Users {
		w.userIndex[user.ID] = i
	}
	w.friends, w.blocked = g.friends, g.blocked
	w.friendList = make([][]int, len(w.Users))
	for i, friends := range g.friends {
		for j := range friends {
			w.friendList[i] = append(w.friendList[i], j)
		}
		slices.Sort(w.friendList[i])
	}
	return w
}

func (g *generator) users() {
	for i := range g.cfg.Users {
		user := &User{
			ID:          g.ids.next(),
			Username:    username(g.r, i),
			DisplayName: choose(g.r, displayNames),
			SecretKey:   secretKey(g.cfg.Seed, i),
			Region:      Regions[pick(g.r, regionWeights)],
			AccountType: "regular",
			Status:      "active",
			Role:        RoleHistory,
			CreatedAt:   between(g.r, g.historyStart.AddDate(-1, 0, 0), g.historyStart),
		}
		switch {
		case i < len(certifiedRegions):
			user.Region = certifiedRegions[i]
			user.AccountType = "certified_public"
			user.Bio = choose(g.r, bios)
			user.Activity = 1
		default:
			if g.r.Float64() < 0.3 {
				user.Bio = choose(g.r, bios)
			}
			user.Activity = []float64{0.9, 0.5, 0.1}[pick(g.r, []float64{0.1, 0.4, 0.5})]
			if g.r.Float64() < 0.02 {
				// 退会は履歴の最終日のあとなので、履歴には普通に参加している。
				deletedAt := g.cfg.EndDate.Add(12 * time.Hour)
				user.Status, user.DeletedAt = "deleted", &deletedAt
			} else if g.r.Float64() < 0.1 {
				user.Role = RoleCohort
			}
		}
		if g.r.Float64() < 0.5 {
			user.ProfileImage = &Image{ID: g.ids.next(), OwnerID: user.ID, Kind: ImageProfile, CreatedAt: user.CreatedAt}
		}
		g.world.Users = append(g.world.Users, user)
	}
	g.friends = make([]map[int]bool, len(g.world.Users))
	for i := range g.friends {
		g.friends[i] = map[int]bool{}
	}
}

func pairKey(a, b int) [2]int {
	return [2]int{min(a, b), max(a, b)}
}

// friendships は優先的選択（友達の多い人ほど新しい友達ができやすい）で友達関係を作る。
// 相手は 8 割の確率で同じリージョンから選ぶ。
func (g *generator) friendships() {
	const perUser = 5
	users := g.world.Users
	var endpoints []int
	for k, i := range g.r.Perm(len(users)) {
		var targets []int
		for attempts := 0; len(targets) < min(perUser, k) && attempts < 100; attempts++ {
			j := endpoints[g.r.IntN(len(endpoints))]
			if j == i || slices.Contains(targets, j) || (users[j].Region != users[i].Region && g.r.Float64() < 0.8) {
				continue
			}
			targets = append(targets, j)
		}
		for _, j := range targets {
			g.befriend(i, j)
			endpoints = append(endpoints, j)
		}
		endpoints = append(endpoints, i)
		for range len(targets) {
			endpoints = append(endpoints, i)
		}
	}
}

func (g *generator) befriend(a, b int) {
	users := g.world.Users
	requester, recipient := users[a], users[b]
	if g.r.IntN(2) == 0 {
		requester, recipient = recipient, requester
	}
	acceptedAt := between(g.r, laterOf(requester.CreatedAt, recipient.CreatedAt), g.historyStart)
	request := &FriendRequest{
		ID: g.ids.next(), RequesterID: requester.ID, RecipientID: recipient.ID, Status: "accepted",
		CreatedAt: between(g.r, laterOf(requester.CreatedAt, recipient.CreatedAt), acceptedAt), UpdatedAt: acceptedAt,
	}
	low, high := requester.ID, recipient.ID
	if high < low {
		low, high = high, low
	}
	g.world.FriendRequests = append(g.world.FriendRequests, request)
	g.world.Friendships = append(g.world.Friendships, &Friendship{
		ID: g.ids.next(), UserLowID: low, UserHighID: high, SourceRequestID: request.ID, CreatedAt: acceptedAt,
	})
	g.friends[a][b] = true
	g.friends[b][a] = true
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// blocks は友達でない 2 人の間にブロックを作る。ブロックした 2 人の間には、この後も
// フォローや友達申請を作らない。
func (g *generator) blocks() {
	users := g.world.Users
	for range len(users) / 20 {
		a, b := g.r.IntN(len(users)), g.r.IntN(len(users))
		if a == b || g.friends[a][b] || g.blocked[pairKey(a, b)] {
			continue
		}
		g.blocked[pairKey(a, b)] = true
		g.world.Blocks = append(g.world.Blocks, &Block{
			ID: g.ids.next(), BlockerID: users[a].ID, BlockedID: users[b].ID,
			CreatedAt: between(g.r, laterOf(users[a].CreatedAt, users[b].CreatedAt), g.historyStart),
		})
	}
}

// openRequests は友達にならなかった申請（保留中、拒否、取り消し）を作る。保留中の申請は
// 1 組の間に 1 件まで。
func (g *generator) openRequests() {
	users := g.world.Users
	pending := map[[2]int]bool{}
	for _, spec := range []struct {
		status string
		count  int
	}{{"pending", len(users) / 2}, {"rejected", len(users) / 5}, {"cancelled", len(users) / 10}} {
		for range spec.count {
			a, b := g.r.IntN(len(users)), g.r.IntN(len(users))
			key := pairKey(a, b)
			if a == b || g.friends[a][b] || g.blocked[key] || (spec.status == "pending" && pending[key]) {
				continue
			}
			pending[key] = pending[key] || spec.status == "pending"
			createdAt := between(g.r, g.historyStart.AddDate(0, 0, -30), g.cfg.EndDate)
			updatedAt := createdAt
			if spec.status != "pending" {
				updatedAt = createdAt.Add(time.Duration(1+g.r.IntN(72)) * time.Hour)
			}
			g.world.FriendRequests = append(g.world.FriendRequests, &FriendRequest{
				ID: g.ids.next(), RequesterID: users[a].ID, RecipientID: users[b].ID,
				Status: spec.status, CreatedAt: createdAt, UpdatedAt: updatedAt,
			})
		}
	}
}

// follows は通常のユーザーに認定公開アカウントを 0〜5 件フォローさせる。上位のアカウント
// ほど選ばれやすい（Zipf 分布）。
func (g *generator) follows() {
	users := g.world.Users
	weights := make([]float64, len(certifiedRegions))
	for k := range weights {
		weights[k] = 1 / float64(k+1)
	}
	for i := len(certifiedRegions); i < len(users); i++ {
		chosen := map[int]bool{}
		for range g.r.IntN(6) {
			k := pick(g.r, weights)
			if chosen[k] || g.blocked[pairKey(i, k)] {
				continue
			}
			chosen[k] = true
			g.world.Follows = append(g.world.Follows, &Follow{
				ID: g.ids.next(), FollowerID: users[i].ID, FolloweeID: users[k].ID,
				CreatedAt: between(g.r, laterOf(users[i].CreatedAt, users[k].CreatedAt), g.historyStart),
			})
		}
	}
}

// windows は全リージョンに毎日 1 回ずつ投稿期間を作る。各期間は同じリージョンの次の期間が
// 始まったときに終わる。最後の期間も終了済みにする。
func (g *generator) windows() {
	last := map[string]*Window{}
	for d := range g.cfg.Days {
		date := g.historyStart.AddDate(0, 0, d)
		for _, region := range Regions {
			startsAt := date.Add(time.Duration(windowBaseHour[region])*time.Hour + time.Duration(g.r.IntN(4*3600))*time.Second)
			window := &Window{ID: g.ids.next(), Region: region, LocalDate: date, StartsAt: startsAt, OnTimeDeadline: startsAt.Add(2 * time.Minute)}
			if previous := last[region]; previous != nil {
				previous.EndedAt = startsAt
			}
			last[region] = window
			g.world.Windows = append(g.world.Windows, window)
		}
	}
	for _, window := range last {
		window.EndedAt = window.StartsAt.Add(6 * time.Hour)
	}
}
