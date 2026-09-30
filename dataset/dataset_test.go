package dataset

import (
	"bytes"
	"encoding/json"
	"image/jpeg"
	"regexp"
	"slices"
	"testing"
	"time"
)

// history は world の全日の投稿、RealMoji、コメント、通知を集める。
type history struct {
	posts         []*Post
	realMojis     []*RealMoji
	comments      []*Comment
	notifications []*Notification
}

func collect(world *World) history {
	var h history
	for day := range world.Config.Days {
		for _, p := range world.PostsOn(day) {
			h.posts = append(h.posts, p)
			h.realMojis = append(h.realMojis, p.RealMojis...)
			h.comments = append(h.comments, p.Comments...)
		}
		h.notifications = append(h.notifications, world.NotificationsOn(day)...)
	}
	return h
}

func TestDeterministic(t *testing.T) {
	cfg := Small()
	cfg.Users, cfg.Days = 200, 5
	marshal := func(cfg Config) []byte {
		world := Generate(cfg)
		data, err := json.Marshal([]any{world, collect(world).posts})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := marshal(cfg)
	if !bytes.Equal(first, marshal(cfg)) {
		t.Fatal("Generate is not deterministic")
	}
	cfg.Seed++
	if bytes.Equal(first, marshal(cfg)) {
		t.Fatal("seed has no effect")
	}
}

// TestPostOnIsLocal は、ある日の投稿を単独で作っても、日の順に作ったときと同じになることを
// 確かめる。bench は履歴の一部だけを作り直すので、ほかの日の生成に依存してはならない。
func TestPostOnIsLocal(t *testing.T) {
	cfg := Small()
	cfg.Users, cfg.Days = 200, 5
	all := collect(Generate(cfg)).posts
	world := Generate(cfg)
	for i := len(all) - 1; i >= 0; i -= 7 {
		p := all[i]
		var user *User
		for _, u := range world.Users {
			if u.ID == p.AuthorID {
				user = u
			}
		}
		day := slices.IndexFunc(world.Windows, func(w *Window) bool { return w.ID == p.WindowID }) / len(Regions)
		got, _ := json.Marshal(world.PostOn(user, day))
		want, _ := json.Marshal(p)
		if !bytes.Equal(got, want) {
			t.Fatalf("post %s differs when generated alone", p.ID)
		}
	}
}

func TestImages(t *testing.T) {
	world := Generate(Config{Seed: 1, Users: 20, Days: 1, EndDate: Small().EndDate, PostImageSize: 64, RealMojiImageSize: 32, ProfileImageSize: 48})
	a, b := &Image{ID: "a", Kind: ImagePost}, &Image{ID: "b", Kind: ImagePost}
	if !bytes.Equal(world.ImageBytes(a), world.ImageBytes(a)) || bytes.Equal(world.ImageBytes(a), world.ImageBytes(b)) {
		t.Fatal("image bytes must be determined by and unique to the image ID")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(world.ImageBytes(&Image{ID: "c", Kind: ImageRealMoji})))
	if err != nil || decoded.Bounds().Dx() != 32 {
		t.Fatalf("realmoji image: %v", err)
	}
}

// TestInvariants は生成したデータがアプリの規則を満たすことを確かめる。
func TestInvariants(t *testing.T) {
	world := Generate(Small())
	h := collect(world)
	users := map[string]*User{}
	usernames := map[string]bool{}
	pattern := regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	for _, user := range world.Users {
		users[user.ID] = user
		if !pattern.MatchString(user.Username) || usernames[user.Username] {
			t.Fatalf("invalid or duplicate username %q", user.Username)
		}
		usernames[user.Username] = true
		if user.AccountType == "certified_public" && (user.Status != "active" || user.Role != RoleHistory) {
			t.Fatalf("certified user %s must be an active history user", user.Username)
		}
	}

	pair := func(a, b string) [2]string { return [2]string{min(a, b), max(a, b)} }
	friends := map[[2]string]bool{}
	friendsOf := map[string]map[string]bool{}
	for _, f := range world.Friendships {
		if f.UserLowID >= f.UserHighID || friends[pair(f.UserLowID, f.UserHighID)] {
			t.Fatalf("friendship %s is not ordered or duplicated", f.ID)
		}
		friends[pair(f.UserLowID, f.UserHighID)] = true
		for _, id := range []string{f.UserLowID, f.UserHighID} {
			if friendsOf[id] == nil {
				friendsOf[id] = map[string]bool{}
			}
		}
		friendsOf[f.UserLowID][f.UserHighID] = true
		friendsOf[f.UserHighID][f.UserLowID] = true
	}
	blocked := map[[2]string]bool{}
	for _, b := range world.Blocks {
		blocked[pair(b.BlockerID, b.BlockedID)] = true
		if friends[pair(b.BlockerID, b.BlockedID)] {
			t.Fatalf("blocked pair %s are friends", b.ID)
		}
	}
	pending := map[[2]string]bool{}
	for _, r := range world.FriendRequests {
		key := pair(r.RequesterID, r.RecipientID)
		if r.Status == "pending" {
			if pending[key] || friends[key] || blocked[key] {
				t.Fatalf("invalid pending request %s", r.ID)
			}
			pending[key] = true
		}
	}
	for _, f := range world.Follows {
		if users[f.FolloweeID].AccountType != "certified_public" || blocked[pair(f.FollowerID, f.FolloweeID)] {
			t.Fatalf("invalid follow %s", f.ID)
		}
	}

	windows := map[string]*Window{}
	for _, w := range world.Windows {
		windows[w.ID] = w
		if !w.EndedAt.After(w.StartsAt) || w.LocalDate.After(world.Config.EndDate) {
			t.Fatalf("window %s is open or after the end date", w.ID)
		}
	}
	posts := map[string]*Post{}
	activePost := map[[2]string]bool{}
	postedOn := map[string]map[time.Time]bool{}
	for _, p := range h.posts {
		posts[p.ID] = p
		w := windows[p.WindowID]
		if users[p.AuthorID].Region != w.Region || p.PostedAt.Before(w.StartsAt) || !p.PostedAt.Before(w.EndedAt) {
			t.Fatalf("post %s is outside its author's window", p.ID)
		}
		if p.DeletedAt == nil {
			key := [2]string{p.AuthorID, w.LocalDate.String()}
			if activePost[key] {
				t.Fatalf("two active posts by %s on %s", p.AuthorID, w.LocalDate)
			}
			activePost[key] = true
			if postedOn[p.AuthorID] == nil {
				postedOn[p.AuthorID] = map[time.Time]bool{}
			}
			postedOn[p.AuthorID][w.LocalDate] = true
		}
	}
	canView := func(viewer string, p *Post) bool {
		if viewer == p.AuthorID {
			return true
		}
		if !postedOn[viewer][windows[p.WindowID].LocalDate] || blocked[pair(viewer, p.AuthorID)] {
			return false
		}
		switch p.Visibility {
		case "public":
			return true
		case "friends":
			return friends[pair(viewer, p.AuthorID)]
		default:
			if friends[pair(viewer, p.AuthorID)] {
				return true
			}
			for friend := range friendsOf[viewer] {
				if friends[pair(friend, p.AuthorID)] {
					return true
				}
			}
			return false
		}
	}
	activeReaction := map[[2]string]bool{}
	for _, r := range h.realMojis {
		p := posts[r.PostID]
		if p.DeletedAt != nil || r.ReactorID == p.AuthorID || !canView(r.ReactorID, p) {
			t.Fatalf("realmoji %s cannot be made by its reactor", r.ID)
		}
		if r.ReplacedAt == nil {
			if activeReaction[[2]string{r.PostID, r.ReactorID}] {
				t.Fatalf("two active realmojis on %s by %s", r.PostID, r.ReactorID)
			}
			activeReaction[[2]string{r.PostID, r.ReactorID}] = true
		}
	}
	for _, c := range h.comments {
		p := posts[c.PostID]
		if !canView(c.AuthorID, p) || (p.Visibility != "public" && !friends[pair(c.AuthorID, p.AuthorID)]) {
			t.Fatalf("comment %s cannot be made by its author", c.ID)
		}
	}

	// DB の timestamptz はマイクロ秒までなので、bench がそのまま比べられるようにそろえておく。
	for _, p := range h.posts {
		if !p.PostedAt.Equal(p.PostedAt.Truncate(time.Microsecond)) {
			t.Fatalf("post %s: postedAt has sub-microsecond precision", p.ID)
		}
	}
	for _, r := range h.realMojis {
		if !r.CreatedAt.Equal(r.CreatedAt.Truncate(time.Microsecond)) {
			t.Fatalf("realmoji %s: createdAt has sub-microsecond precision", r.ID)
		}
	}

	ids := map[string]bool{}
	for _, id := range slices.Concat(
		mapIDs(h.posts, func(p *Post) string { return p.ID }), mapIDs(h.posts, func(p *Post) string { return p.Image.ID }),
		mapIDs(h.realMojis, func(r *RealMoji) string { return r.ID }), mapIDs(h.realMojis, func(r *RealMoji) string { return r.Image.ID }),
		mapIDs(h.comments, func(c *Comment) string { return c.ID }), mapIDs(h.notifications, func(n *Notification) string { return n.ID }),
	) {
		if ids[id] {
			t.Fatalf("duplicate ID %s", id)
		}
		ids[id] = true
	}

	t.Logf("users=%d friendships=%d requests=%d follows=%d blocks=%d windows=%d posts=%d realmojis=%d comments=%d notifications=%d",
		len(world.Users), len(world.Friendships), len(world.FriendRequests), len(world.Follows), len(world.Blocks), len(world.Windows),
		len(h.posts), len(h.realMojis), len(h.comments), len(h.notifications))
}

func mapIDs[T any](items []T, id func(T) string) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = id(item)
	}
	return ids
}
