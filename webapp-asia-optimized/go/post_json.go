package isureal

import (
	"bytes"
	"sync"
	"time"

	gojson "github.com/goccy/go-json"
)

// フィードで返す投稿の JSON を投稿ごとにキャッシュする。
// 投稿・投稿者の表示に関わる項目と画像 URL の exp がすべて同じときだけ再利用する。
type postJSONEntry struct {
	exp    int64
	post   Post
	author User
	data   []byte
}

var postJSONCache sync.Map // post id -> *postJSONEntry

func currentImageExp() int64 {
	return time.Now().Add(imageURLLifetime).Unix() / imageExpQuantum * imageExpQuantum
}

func samePostJSON(a, b *Post) bool {
	return a.ID == b.ID && a.AuthorID == b.AuthorID && a.PostingWindowID == b.PostingWindowID && a.ImageID == b.ImageID &&
		a.Caption == b.Caption && a.Visibility == b.Visibility && a.PostLocation.Kind == b.PostLocation.Kind &&
		eqPtr(a.Latitude, b.Latitude) && eqPtr(a.Longitude, b.Longitude) && eqPtr(a.Locality, b.Locality) &&
		a.PostedAt.Equal(b.PostedAt) && a.PostedAt.Location() == b.PostedAt.Location() && a.OnTimeDeadline.Equal(b.OnTimeDeadline)
}

func sameAuthorJSON(a, b *User) bool {
	return a.ID == b.ID && a.Username == b.Username && a.DisplayName == b.DisplayName && a.Bio == b.Bio &&
		a.AccountType == b.AccountType && eqPtr(a.ProfileImageID, b.ProfileImageID) && bytes.Equal(a.ImageKey, b.ImageKey)
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func postJSON(post Post, author User) ([]byte, error) {
	exp := currentImageExp()
	if v, ok := postJSONCache.Load(post.ID); ok {
		e := v.(*postJSONEntry)
		if e.exp == exp && samePostJSON(&e.post, &post) && sameAuthorJSON(&e.author, &author) {
			return e.data, nil
		}
	}
	data, err := gojson.Marshal(newPostResponse(post, author))
	if err != nil {
		return nil, err
	}
	postJSONCache.Store(post.ID, &postJSONEntry{exp: exp, post: post, author: author, data: data})
	return data, nil
}

func resetPostJSONCache() {
	postJSONCache.Clear()
}
