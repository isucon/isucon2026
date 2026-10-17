package model

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// randomWorld は、並行する書き込み（送信中、拒否、結果不明を含む）を無作為に重ねたモデル。
type randomWorld struct {
	m     *Model
	rng   *rand.Rand
	users []string
	posts []string
	start time.Time
	end   time.Time
}

func newRandomWorld(seed uint64) *randomWorld {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := start
	m := New()
	m.now = func() time.Time { return now }
	w := &randomWorld{m: m, rng: rng, start: start}
	tick := func() { now = now.Add(time.Duration(rng.IntN(300)) * time.Millisecond) }
	regions := []string{"asia", "europe"}
	for i := range 24 {
		id := fmt.Sprintf("u%02d", i)
		w.users = append(w.users, id)
		m.AddUser(User{ID: id, Username: id, SecretKey: "k" + id, Region: regions[i%len(regions)]})
	}
	settle := func(write *Write) {
		switch rng.IntN(5) {
		case 0, 1, 2:
			write.Applied()
		case 3:
			write.Rejected()
		case 4:
			if rng.IntN(2) == 0 {
				write.Unknown()
			} // それ以外は送信中のまま
		}
	}
	visibilities := []string{"public", "friends", "friends_of_friends"}
	windows := 0
	for step := range 400 {
		tick()
		switch op := rng.IntN(10); {
		case op < 3:
			if a, b := w.user(), w.user(); a != b {
				settle(m.BeginFriendship(a, b, rng.IntN(4) != 0))
			}
		case op == 3:
			if a, b := w.user(), w.user(); a != b && rng.IntN(3) == 0 {
				settle(m.BeginBlock(a, b))
			} else if a != b {
				settle(m.BeginFollow(a, b))
			}
		case op == 4 && step%40 == 0:
			region := regions[rng.IntN(len(regions))]
			open := m.BeginOpenWindow(region)
			tick()
			if rng.IntN(6) != 0 {
				windows++
				m.OpenedWindow(open, Window{ID: fmt.Sprintf("w%d", windows), Region: region, LocalDate: start.AddDate(0, 0, windows)})
			}
		case op < 8:
			author := w.user()
			current := m.current[m.users[author].Region]
			if current == nil || current.changes[len(current.changes)-1].value == "?" {
				continue
			}
			creation := m.BeginCreatePost(author)
			tick()
			switch rng.IntN(6) {
			case 0:
				creation.Unknown()
			case 1:
				creation.Rejected()
			case 2:
			default:
				id := fmt.Sprintf("p%03d", len(w.posts))
				w.posts = append(w.posts, id)
				m.Created(creation, Post{ID: id, AuthorID: author, WindowID: current.changes[len(current.changes)-1].value, ImageID: "img" + id, Caption: "c" + id, PostedAt: now}, visibilities[rng.IntN(3)])
			}
		case op == 8 && len(w.posts) > 0:
			id := w.posts[rng.IntN(len(w.posts))]
			if rng.IntN(2) == 0 {
				settle(m.BeginDeletePost(id))
			} else {
				settle(m.BeginUpdateVisibility(id, visibilities[rng.IntN(3)]))
			}
		}
	}
	w.end = now
	return w
}

func (w *randomWorld) user() string { return w.users[w.rng.IntN(len(w.users))] }

// read は走行中の無作為な時点の読み取り。
func (w *randomWorld) read() Read {
	sent := w.start.Add(time.Duration(w.rng.Int64N(int64(w.end.Sub(w.start)) + 1)))
	return Read{Sent: sent, Received: sent.Add(time.Duration(w.rng.IntN(800)) * time.Millisecond), Staleness: time.Duration(w.rng.IntN(3)) * 300 * time.Millisecond}
}

// TestFeedCandidates は、欠落の検査が索引で絞った候補に、フィードに出るかもしれない
// （判定が偽でない）投稿をすべて含むことを確かめる。
func TestFeedCandidates(t *testing.T) {
	for seed := range uint64(30) {
		w := newRandomWorld(seed)
		m := w.m
		for range 50 {
			viewer, r := w.user(), w.read()
			for _, feed := range []string{"public", "friends", "friends-of-friends", "following"} {
				candidates := map[string]bool{}
				m.feedCandidates(viewer, feed, r, func(p *Post) { candidates[p.ID] = true })
				v := m.newView(m.users[viewer], r)
				for _, id := range m.openWindows(r) {
					for _, p := range m.byWindow[id] {
						if got := v.inFeedTri(p, feed); got != False && !candidates[p.ID] {
							t.Fatalf("seed %d: %s's %s feed misses candidate %s (%s)", seed, viewer, feed, p.ID, got)
						}
					}
				}
			}
		}
	}
}

func TestRejectedCreationIsDropped(t *testing.T) {
	m, c := newTestModel()
	m.AddUser(User{ID: "a", Region: "asia"})
	rejected := m.BeginCreatePost("a")
	unknown := m.BeginCreatePost("a")
	c.tick()
	rejected.Rejected()
	unknown.Unknown()
	if got := len(m.pending["a"]); got != 1 {
		t.Fatalf("pending creations = %d, want only the unknown one", got)
	}
	if !m.mayHaveUnknownPost("a", m.ReadBetween(c.tick(), c.tick())) {
		t.Fatal("a creation with an unknown outcome must stay")
	}
}
