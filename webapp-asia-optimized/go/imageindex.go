package isureal

import (
	"context"
	"database/sql"
	"encoding/hex"
	"sync"

	"github.com/jmoiron/sqlx"
)

// レスポンスで署名付き URL を返した画像（と起動後に作られた画像）の参照先。
// 画像の表示可否を DB を引かずに判定するために使う（ここにない画像は DB で判定する）。
// 表示可否を変える操作（投稿削除・RealMoji の置換と削除・プロフィール画像の変更・退会）はすべてここに反映される。
// ID はすべて UUID なので 16 バイトの配列をキーにし、ポインタを含まないマップにして GC の走査対象から外す。
// UUID として読めない ID は登録しない（DB で判定される）。
type uuidKey [16]byte

const (
	imageKindPost uint8 = iota + 1
	imageKindRealMoji
	imageKindProfile
)

type runtimeImage struct {
	Kind    uint8
	Hidden  bool // RealMoji が置き換え・削除された
	HasPost bool
	Owner   uuidKey
	Post    uuidKey
}

const imageIndexShards = 64

type imageIndexShard struct {
	mu      sync.RWMutex
	images  map[uuidKey]runtimeImage
	authors map[uuidKey]uuidKey  // post id -> author id（不変）
	deleted map[uuidKey]struct{} // 起動後に削除された投稿
}

var imageIndex [imageIndexShards]imageIndexShard

func init() {
	resetRuntimeImages()
}

func shardOf(k uuidKey) *imageIndexShard {
	return &imageIndex[k[15]%imageIndexShards]
}

// 小文字の UUID 文字列だけを受け付ける（大文字を同じキーにすると DB の判定と食い違うため）
func parseUUIDKey(s string) (uuidKey, bool) {
	var k uuidKey
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return k, false
	}
	var buf [32]byte
	n := 0
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return k, false
		}
		buf[n] = c
		n++
	}
	if _, err := hex.Decode(k[:], buf[:]); err != nil {
		return k, false
	}
	return k, true
}

func (k uuidKey) String() string {
	var buf [36]byte
	formatUUID(buf[:], k[:])
	return string(buf[:])
}

// 画像の所有者の ID を毎回文字列に戻さないように使い回す
var userIDStrings sync.Map // uuidKey -> string

func userIDString(k uuidKey) string {
	if v, ok := userIDStrings.Load(k); ok {
		return v.(string)
	}
	s := k.String()
	userIDStrings.Store(k, s)
	return s
}

func resetRuntimeImages() {
	for i := range imageIndex {
		s := &imageIndex[i]
		s.mu.Lock()
		s.images = map[uuidKey]runtimeImage{}
		s.authors = map[uuidKey]uuidKey{}
		s.deleted = map[uuidKey]struct{}{}
		s.mu.Unlock()
	}
}

func registerRuntimeImage(imageID string, kind uint8, ownerID, postID string) {
	img, ok := parseUUIDKey(imageID)
	if !ok {
		return
	}
	rec := runtimeImage{Kind: kind}
	if rec.Owner, ok = parseUUIDKey(ownerID); !ok {
		return
	}
	if postID != "" {
		if rec.Post, ok = parseUUIDKey(postID); !ok {
			return
		}
		rec.HasPost = true
	}
	s := shardOf(img)
	s.mu.RLock()
	_, exists := s.images[img]
	s.mu.RUnlock()
	if exists {
		return
	}
	s.mu.Lock()
	if _, exists := s.images[img]; !exists {
		s.images[img] = rec
	}
	s.mu.Unlock()
}

func storePostAuthor(postID, authorID string) {
	p, ok1 := parseUUIDKey(postID)
	a, ok2 := parseUUIDKey(authorID)
	if !ok1 || !ok2 {
		return
	}
	s := shardOf(p)
	s.mu.RLock()
	_, exists := s.authors[p]
	s.mu.RUnlock()
	if exists {
		return
	}
	s.mu.Lock()
	s.authors[p] = a
	s.mu.Unlock()
}

func registerPostImage(post Post) {
	storePostAuthor(post.ID, post.AuthorID)
	registerRuntimeImage(post.ImageID, imageKindPost, post.AuthorID, post.ID)
}

func registerRealMojiImage(reaction RealMoji) {
	registerRuntimeImage(reaction.ImageID, imageKindRealMoji, reaction.ReactorID, reaction.PostID)
}

func registerProfileImage(user User) {
	if user.ProfileImageID != nil {
		registerRuntimeImage(*user.ProfileImageID, imageKindProfile, user.ID, "")
	}
}

// 置き換え・削除された RealMoji の画像は表示しない（未登録なら墓標を置く）
func hideRuntimeImages(imageIDs ...string) {
	for _, id := range imageIDs {
		img, ok := parseUUIDKey(id)
		if !ok {
			continue
		}
		s := shardOf(img)
		s.mu.Lock()
		rec, exists := s.images[img]
		if !exists {
			rec = runtimeImage{Kind: imageKindRealMoji}
		}
		rec.Hidden = true
		s.images[img] = rec
		s.mu.Unlock()
	}
}

func markPostDeleted(postID string) {
	p, ok := parseUUIDKey(postID)
	if !ok {
		return
	}
	s := shardOf(p)
	s.mu.Lock()
	s.deleted[p] = struct{}{}
	s.mu.Unlock()
}

func isPostDeleted(p uuidKey) bool {
	s := shardOf(p)
	s.mu.RLock()
	_, ok := s.deleted[p]
	s.mu.RUnlock()
	return ok
}

func postAuthorID(ctx context.Context, db sqlx.QueryerContext, postID string) (string, error) {
	if p, ok := parseUUIDKey(postID); ok {
		s := shardOf(p)
		s.mu.RLock()
		a, ok := s.authors[p]
		s.mu.RUnlock()
		if ok {
			return userIDString(a), nil
		}
	}
	var authorID string
	if err := sqlx.GetContext(ctx, db, &authorID, `SELECT author_id FROM posts WHERE id = $1`, postID); err != nil {
		return "", err
	}
	storePostAuthor(postID, authorID)
	return authorID, nil
}

// 登録済みの画像ならメモリだけで表示可否を判定する
func runtimeImageMeta(ctx context.Context, db sqlx.QueryerContext, imageID string) (imageMeta, bool, error) {
	key, ok := parseUUIDKey(imageID)
	if !ok {
		return imageMeta{}, false, nil
	}
	s := shardOf(key)
	s.mu.RLock()
	img, ok := s.images[key]
	s.mu.RUnlock()
	if !ok {
		return imageMeta{}, false, nil
	}
	if img.Hidden {
		return imageMeta{}, true, sql.ErrNoRows
	}
	ownerID := userIDString(img.Owner)
	owner, err := getUserAnyStatusCached(ctx, db, ownerID)
	if err != nil {
		return imageMeta{}, true, err
	}
	meta := imageMeta{OwnerID: ownerID, SecretKeyHash: owner.SecretKeyHash, MediaType: "image/jpeg"}
	postDeleted := img.HasPost && isPostDeleted(img.Post)
	switch img.Kind {
	case imageKindPost:
		meta.Visible = !postDeleted
	case imageKindRealMoji:
		if !postDeleted {
			ps := shardOf(img.Post)
			ps.mu.RLock()
			a, ok := ps.authors[img.Post]
			ps.mu.RUnlock()
			authorID := userIDString(a)
			if !ok {
				if authorID, err = postAuthorID(ctx, db, img.Post.String()); err != nil {
					return imageMeta{}, true, err
				}
			}
			author, err := getUserAnyStatusCached(ctx, db, authorID)
			if err != nil {
				return imageMeta{}, true, err
			}
			meta.Visible = author.Status == "active"
		}
	case imageKindProfile:
		meta.Visible = owner.ProfileImageID != nil && *owner.ProfileImageID == imageID
	}
	return meta, true, nil
}
