package dataset

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

var syllables = []string{
	"ka", "ki", "ku", "ke", "ko", "sa", "shi", "su", "se", "so", "ta", "chi", "tsu", "te", "to",
	"na", "ni", "nu", "ne", "no", "ha", "hi", "fu", "he", "ho", "ma", "mi", "mu", "me", "mo",
	"ya", "yu", "yo", "ra", "ri", "ru", "re", "ro", "wa", "an", "el", "is", "or", "ul", "ben",
	"dan", "lia", "mar", "sol", "tor", "vin", "zoe", "leo", "mia", "noa", "ivy", "max", "eva",
}

// displayNames は表示名の元。検索の正規化（NFC と大文字小文字の畳み込み）を確かめられる
// よう、日本語、アクセント付き文字、合成済みでない文字（e + U+0301）を混ぜる。
var displayNames = []string{
	"Kana Miro", "Leo Tanaka", "Mia Schmidt", "Noah Smith", "Ivy Chen", "Max Müller",
	"佐藤 花子", "鈴木 一郎", "高橋 さくら", "田中 ゆうと", "伊藤 美咲", "渡辺 蓮",
	"José Álvarez", "Zoë Dubois", "Björn Larsson", "Chloé Martin", "Renée Laurent", "Dvořák Ema",
	"José García", "Zoë Müller", "ＫＡＮＡ", "ÅSA", "Ǆemal", "straße",
}

var bios = []string{
	"毎日の空を撮っています", "Coffee, cameras and cats.", "東京在住。散歩が好き",
	"Berlin based. Night owl.", "旅行とラーメン", "Running every morning",
	"Photographer. Opinions are my own.", "週末はキャンプ",
}

var captions = []string{
	"今日のランチ", "morning walk", "やっと晴れた", "at the office again", "猫がいた",
	"sunset!", "電車が遅れてる", "new shoes", "雨の日", "coffee break", "夜ふかし中",
	"weekend vibes", "がんばった", "long day", "ひさしぶり", "",
}

// commentBodies は負荷走行中のコメントの本文。初期データの、どの椅子にも付く一言から日本語と
// 英語を交互に選んでいる。
var commentBodies = []string{
	"いい椅子！", "Looks comfy", "どこの椅子？", "I want one", "座ってみたい", "Love the color", "わかる",
	"Wow", "かわいい", "Classy", "落ち着く", "Save me a seat", "👀", "🔥🔥",
}

var localities = []string{"Shibuya", "Shinjuku", "Umeda", "Kreuzberg", "Montmartre", "Soho", "Brooklyn", "Mission District"}

var reactionTypes = []string{"like", "smile", "surprise", "heart", "laugh", "instant"}

// username は [a-z0-9_.-] だけからなり、通し番号を含むので重複しない。
func username(r *rand.Rand, index int) string {
	var b strings.Builder
	for range 2 + r.IntN(2) {
		b.WriteString(syllables[r.IntN(len(syllables))])
	}
	separators := []string{"", ".", "_", "-"}
	return fmt.Sprintf("%s%s%d", b.String(), separators[r.IntN(len(separators))], index)
}

func choose[T any](r *rand.Rand, values []T) T {
	return values[r.IntN(len(values))]
}
