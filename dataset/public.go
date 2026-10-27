package dataset

import "math/rand/v2"

// Username は初期データのユーザーと同じ形のユーザー名を返す。index が初期データの
// ユーザー数以上なら初期データのユーザー名と重ならない。
func Username(r *rand.Rand, index int) string {
	return username(r, index)
}

// DisplayName は初期データと同じ候補から表示名を選ぶ。
func DisplayName(r *rand.Rand) string {
	return choose(r, displayNames)
}

// Bio は初期データと同じ候補から自己紹介を選ぶ。
func Bio(r *rand.Rand) string {
	return choose(r, bios)
}

// CommentBody は初期データと同じ候補からコメントの本文を選ぶ。空の本文は返さない。
func CommentBody(r *rand.Rand) string {
	return choose(r, commentBodies)
}

// ReactionType は RealMoji の種類を選ぶ。
func ReactionType(r *rand.Rand) string {
	return choose(r, reactionTypes)
}
