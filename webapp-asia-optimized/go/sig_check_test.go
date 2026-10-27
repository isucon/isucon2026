package isureal

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	gojson "github.com/goccy/go-json"
)

func TestFastImageSignature(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	for _, id := range []string{"0190f3a0-1111-2222-3333-444455556666", "x", string(bytes.Repeat([]byte("y"), 70))} {
		exp := int64(1760000000)
		want := imageSignature(key, id, exp)
		got := fastImageSignature(key, []byte(strconv.FormatInt(exp, 10)+":"+id))
		if !bytes.Equal(want, got) {
			t.Fatalf("mismatch for %q", id)
		}
	}
}

func BenchmarkFastImageSignature(b *testing.B) {
	key := bytes.Repeat([]byte{0xab}, 32)
	v := []byte("1760000000:0190f3a0-1111-2222-3333-444455556666")
	b.ReportAllocs()
	for b.Loop() {
		fastImageSignature(key, v)
	}
}

func TestFeedJSON(t *testing.T) {
	lat, loc := 35.5, "東京 <&>"
	pid := "0190f3a0-1111-2222-3333-444455556677"
	posts := []Post{
		{ID: "p1", AuthorID: "a1", PostingWindowID: "w1", ImageID: "0190f3a0-1111-2222-3333-444455556666", Caption: "hi \"x\" <b>&", Visibility: "public",
			PostLocation: PostLocation{Kind: "point", Latitude: &lat, Longitude: &lat, Locality: &loc},
			PostedAt:     time.Now(), OnTimeDeadline: time.Now().Add(-time.Minute)},
		{ID: "p2", AuthorID: "a1", Caption: "", Visibility: "friends", PostLocation: PostLocation{Kind: "none"},
			PostedAt: time.Now().UTC(), OnTimeDeadline: time.Now().Add(time.Hour)},
	}
	author := User{ID: "a1", Username: "u", DisplayName: "D<>", Bio: "b ", AccountType: "personal", ProfileImageID: &pid, ImageKey: bytes.Repeat([]byte{1}, 32)}
	for _, hasMore := range []bool{true, false} {
		for n := 0; n <= len(posts); n++ {
			var want bytes.Buffer
			resp := getFeedResponse{Items: []createPostResponse{}, HasMore: hasMore}
			var items [][]byte
			for _, p := range posts[:n] {
				resp.Items = append(resp.Items, newPostResponse(p, author))
				d, err := postJSON(p, author)
				if err != nil {
					t.Fatal(err)
				}
				items = append(items, d)
			}
			if err := gojson.NewEncoder(&want).Encode(resp); err != nil {
				t.Fatal(err)
			}
			if got := buildFeedJSON(items, hasMore); !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("mismatch\n%s\n%s", got, want.Bytes())
			}
		}
	}
}

func TestCompact(t *testing.T) {
	lat, lon, loc := 1.5, 2.5, "loc"
	now := time.Now().Round(time.Microsecond)
	posts := []Post{{ID: "a", AuthorID: "x", Caption: "c1", Visibility: "public", PostLocation: PostLocation{Kind: "exact", Latitude: &lat, Longitude: &lon, Locality: &loc}, PostedAt: now},
		{ID: "b", Caption: "", Visibility: "friends", PostedAt: now.UTC()}}
	l := newAuthorPostList(posts)
	for i := range posts {
		got := l.post(i, posts[i].AuthorID)
		a, _ := gojson.Marshal(newPostResponse(got, User{}))
		b, _ := gojson.Marshal(newPostResponse(posts[i], User{}))
		if string(a) != string(b) {
			t.Fatalf("%s\n%s", a, b)
		}
	}
	in := postInteractions{RealMojis: []RealMoji{{ID: "r", PostID: "p", ReactorID: "u", ImageID: "i", ReactionType: "like", CreatedAt: now}},
		Comments: []Comment{{ID: "c", PostID: "p", AuthorID: "u", Body: "body", CreatedAt: now}}}
	c := newCompactInteractions(in)
	out := c.expand()
	if out.RealMojis[0].ReactorID != "u" || out.Comments[0].Body != "body" || !out.Comments[0].CreatedAt.Equal(now) || out.RealMojis[0].CreatedAt.Location() != now.Location() {
		t.Fatalf("%+v", out)
	}
}

func TestUUIDKey(t *testing.T) {
	id := "c55745fb-68eb-481d-8ecb-5cc8053628d7"
	k, ok := parseUUIDKey(id)
	if !ok || k.String() != id {
		t.Fatal(k.String())
	}
	if _, ok := parseUUIDKey("C55745FB-68eb-481d-8ecb-5cc8053628d7"); ok {
		t.Fatal("upper")
	}
	registerRuntimeImage(id, imageKindPost, id, id)
	markPostDeleted(id)
	s := shardOf(k)
	if !s.images[k].HasPost || !isPostDeleted(k) {
		t.Fatal("index")
	}
}

func TestSignedImageURL(t *testing.T) {
	u := User{ImageKey: bytes.Repeat([]byte{7}, 32)}
	id := "c55745fb-68eb-481d-8ecb-5cc8053628d7"
	got := signedImageURL(u, id)
	exp := currentImageExp()
	q := url.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {base64.RawURLEncoding.EncodeToString(imageSignature(u.ImageKey, id, exp))}}
	if want := "/api/images/" + url.PathEscape(id) + "?" + q.Encode(); got != want {
		t.Fatalf("%s\n%s", got, want)
	}
	if got2 := signedImageURL(u, id); got2 != got {
		t.Fatal("cache")
	}
}

func TestKeyLocks(t *testing.T) {
	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				unlock := lockKeys("a", "b", strconv.Itoa(i%3))
				counter++
				unlock()
			}
		}(i)
	}
	wg.Wait()
	if counter != 50*200 {
		t.Fatal(counter)
	}
	for i := range keyLocks {
		if len(keyLocks[i].m) != 0 {
			t.Fatal("leak")
		}
	}
}
