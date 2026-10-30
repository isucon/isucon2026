package dataset

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/jpeg"
	"slices"
	"strings"
	"sync"
)

// photos/ には椅子の写真のアーカイブ（bench が投稿に使う merged.zip）を縮小した写真と、その
// 一覧を置いている。写真は cmd/chairphotos で作り直せる。
//
//   - catalog.tsv は写真ごとのリージョン、テーマ、各言語のタイトル。タイトルの日本語は
//     アーカイブの manifest.json の title_ja で、ほかの言語はこのデータセット用に訳した。
//   - comments.tsv はテーマごとのコメントを各言語で書いたもの。
//
//go:embed photos
var photoFiles embed.FS

// languageCodes は TSV の言語の列の順。
var languageCodes = []string{"ja", "zh", "ko", "en", "fr", "de", "es", "pt"}

// noveltyTheme は椅子のダジャレの写真（ダイス、アイスなど）のテーマ。
const noveltyTheme = "novelty"

// chair は画像の元にする写真 1 枚。
type chair struct {
	file   string
	region string
	theme  string
	// titles は言語コードごとのタイトル。投稿のキャプションに使う。
	titles map[string]string

	once  sync.Once
	image *image.YCbCr
}

// decoded は写真を読んで返す。読んだ写真は覚えておく（全部で約 50 MB）。
func (c *chair) decoded() *image.YCbCr {
	c.once.Do(func() {
		data, err := photoFiles.ReadFile("photos/" + c.file)
		if err != nil {
			panic(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			panic(fmt.Sprintf("%s: %v", c.file, err))
		}
		c.image = img.(*image.YCbCr)
	})
	return c.image
}

var (
	// plainChairs は変わり種を除いた写真。
	plainChairs []*chair
	// regionChairs はリージョンごとの、変わり種を除いた写真。
	regionChairs = map[string][]*chair{}
	// noveltyChairs は変わり種の写真。
	noveltyChairs []*chair
	// themeComments はテーマと言語コードごとのコメント。
	themeComments = map[string]map[string][]string{}
)

func init() {
	for _, row := range readTSV("photos/catalog.tsv", 3+len(languageCodes)) {
		c := &chair{file: row[0], region: row[1], theme: row[2], titles: map[string]string{}}
		for i, code := range languageCodes {
			c.titles[code] = row[3+i]
		}
		if !slices.Contains(Regions, c.region) {
			panic(fmt.Sprintf("catalog.tsv: %s: unknown region %q", c.file, c.region))
		}
		if c.theme == noveltyTheme {
			noveltyChairs = append(noveltyChairs, c)
			continue
		}
		plainChairs = append(plainChairs, c)
		regionChairs[c.region] = append(regionChairs[c.region], c)
	}
	for _, row := range readTSV("photos/comments.tsv", 1+len(languageCodes)) {
		if themeComments[row[0]] == nil {
			themeComments[row[0]] = map[string][]string{}
		}
		for i, code := range languageCodes {
			themeComments[row[0]][code] = append(themeComments[row[0]][code], row[1+i])
		}
	}
}

// readTSV は見出しの行を除いた行を返す。どの行も columns 列で、空の値を持たない。
func readTSV(name string, columns int) [][]string {
	data, err := photoFiles.ReadFile(name)
	if err != nil {
		panic(err)
	}
	var rows [][]string
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if i == 0 {
			continue
		}
		row := strings.Split(line, "\t")
		if len(row) != columns || slices.Contains(row, "") {
			panic(fmt.Sprintf("%s:%d: want %d non-empty columns, got %q", name, i+1, columns, row))
		}
		rows = append(rows, row)
	}
	return rows
}
