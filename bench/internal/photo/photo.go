// Package photo は投稿に使う写真を用意する。
//
// 写真は椅子の写真のアーカイブ（ZIP）から読む。アーカイブはローカルのファイルか、
// http(s) の URL（S3 の署名付き URL など）で渡す。各セットの manifest.json にある
// ファイル名、地域（アジア／ヨーロッパ／アメリカ）、日本語のタイトルを使い、投稿者の
// 地域に合う写真を選んでタイトルをキャプションにする。
//
// 同じ写真を何人もが投稿するので、投稿ごとに COM セグメントへ固有の印を差し込み、さらに
// エントロピー符号化データのビットを少し反転して、画素まで異なる写真にする（scan.go）。
// 実際の写真は 1 枚ずつ異なる。再エンコードしないので bench の CPU はほとんど使わない。
//
// アーカイブを渡さないときは、小さなノイズ画像を生成して代わりに使う（開発用）。
package photo

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	jpegStd "image/jpeg"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/imagegen"
)

// Photo はアーカイブの写真 1 枚。
type Photo struct {
	Name    string
	Region  string
	Title   string
	Novelty bool
	jpeg
}

// jpeg は印を差し込める JPEG。insert は SOI と APPn セグメントの直後の位置。flips は
// 投稿ごとに反転するビットの候補（baseline でない JPEG では nil）。
type jpeg struct {
	data   []byte
	insert int
	flips  flips
}

// Set は投稿、RealMoji、プロフィールに使う画像の集まり。読み込んだあとは読み取り専用。
type Set struct {
	photos   []*Photo
	byRegion map[string][]*Photo
	novelty  []*Photo
	// reactions と profiles は写真から作った小さな JPEG。
	reactions []jpeg
	profiles  []jpeg
	generated bool
}

// 小さな画像の大きさ。アプリは RealMoji とプロフィール画像に 512 px 以下を求める。
const (
	reactionSide = 160
	profileSide  = 256
	derivedCount = 24
)

// noveltyRate は投稿が変わり種の写真になる割合。
const noveltyRate = 0.05

var regionNames = map[string]string{"アジア": "asia", "ヨーロッパ": "europe", "アメリカ": "america"}

// Load は source（ファイルのパスか http(s) の URL）からアーカイブを読む。
func Load(ctx context.Context, source string) (*Set, error) {
	data, err := fetch(ctx, source)
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("写真のアーカイブを開けません: %w", err)
	}
	type entry struct {
		Filename      string `json:"filename"`
		SettingRegion string `json:"setting_region"`
		TitleJA       string `json:"title_ja"`
		Separate      bool   `json:"collection_is_separate"`
	}
	meta := map[string]entry{}
	files := map[string]*zip.File{}
	for _, f := range archive.File {
		name := path.Base(f.Name)
		switch {
		case name == "manifest.json":
			var entries []entry
			if err := readJSON(f, &entries); err != nil {
				return nil, fmt.Errorf("%s を読めません: %w", f.Name, err)
			}
			for _, e := range entries {
				meta[e.Filename] = e
			}
		case strings.HasSuffix(strings.ToLower(name), ".jpg") && !strings.Contains(f.Name, "contact"):
			files[name] = f
		}
	}
	set := &Set{byRegion: map[string][]*Photo{}}
	for name, f := range files {
		e, ok := meta[name]
		if !ok {
			continue
		}
		raw, err := readAll(f)
		if err != nil {
			return nil, fmt.Errorf("%s を読めません: %w", f.Name, err)
		}
		insert, err := insertPoint(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		p := &Photo{Name: name, Region: regionNames[e.SettingRegion], Title: e.TitleJA, Novelty: e.Separate, jpeg: jpeg{data: raw, insert: insert}}
		set.photos = append(set.photos, p)
	}
	if len(set.photos) == 0 {
		return nil, fmt.Errorf("アーカイブに写真がありません")
	}
	if err := findAllFlips(set.photos); err != nil {
		return nil, err
	}
	// 走行ごとに選び方を変えないよう、名前の順に並べる。
	slices.SortFunc(set.photos, func(a, b *Photo) int { return strings.Compare(a.Name, b.Name) })
	for _, p := range set.photos {
		if p.Novelty {
			set.novelty = append(set.novelty, p)
		} else {
			set.byRegion[p.Region] = append(set.byRegion[p.Region], p)
		}
	}
	if err := set.derive(); err != nil {
		return nil, err
	}
	return set, nil
}

// Generated はアーカイブの代わりに生成した小さな画像を使う Set を返す。
func Generated() *Set {
	return &Set{generated: true}
}

// Count は写真の数。
func (s *Set) Count() int { return len(s.photos) }

// Markable は投稿ごとに画素まで変えられる写真の数。baseline でない JPEG や、反転できる
// 係数のない写真は COM セグメントの印だけになり、写真の中身で重複を除けてしまう。
func (s *Set) Markable() int {
	n := 0
	for _, p := range s.photos {
		if slices.ContainsFunc(p.flips, func(candidates []flip) bool { return len(candidates) > 0 }) {
			n++
		}
	}
	return n
}

// IsGenerated は生成した画像を使う Set か。
func (s *Set) IsGenerated() bool { return s.generated }

// Post は region のユーザーの投稿の画像とキャプションを返す。key ごとに異なるバイト列になる。
func (s *Set) Post(region string, key uint64) ([]byte, string) {
	if s.generated {
		data, err := imagegen.JPEG(64, 64, key)
		if err != nil {
			panic(err)
		}
		return data, "今日の椅子"
	}
	candidates := s.byRegion[region]
	if len(candidates) == 0 || (len(s.novelty) > 0 && float64(key%1000)/1000 < noveltyRate) {
		candidates = s.novelty
	}
	if len(candidates) == 0 {
		candidates = s.photos
	}
	p := candidates[(key/1000)%uint64(len(candidates))]
	return p.mark(key), p.Title
}

// RealMoji は RealMoji の画像を返す。key ごとに異なるバイト列になる。
func (s *Set) RealMoji(key uint64) []byte {
	if s.generated {
		data, err := imagegen.JPEG(32, 32, key)
		if err != nil {
			panic(err)
		}
		return data
	}
	return s.reactions[key%uint64(len(s.reactions))].mark(key)
}

// Profile はプロフィール画像を返す。key ごとに異なるバイト列になる。
func (s *Set) Profile(key uint64) []byte {
	if s.generated {
		data, err := imagegen.JPEG(48, 48, key)
		if err != nil {
			panic(err)
		}
		return data
	}
	return s.profiles[key%uint64(len(s.profiles))].mark(key)
}

// mark は JPEG に key を書いた COM セグメントを差し込み、key で選んだビットを反転した
// コピーを返す。
func (j jpeg) mark(key uint64) []byte {
	payload := fmt.Appendf(nil, "ISUReal %016x", key)
	// 反転で 0xFF になったバイトにはスタッフィングの 0x00 が付く。
	out := make([]byte, 0, len(j.data)+4+len(payload)+len(j.flips)/128+16)
	out = append(out, j.data[:j.insert]...)
	out = append(out, 0xFF, 0xFE)
	out = binary.BigEndian.AppendUint16(out, uint16(2+len(payload)))
	out = append(out, payload...)
	return j.flips.apply(out, j.data, j.insert, key)
}

// findAllFlips は写真ごとに反転できるビットを並列に集める。
func findAllFlips(photos []*Photo) error {
	errs := make([]error, len(photos))
	limit := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i, p := range photos {
		limit <- struct{}{}
		wg.Go(func() {
			defer func() { <-limit }()
			p.flips, errs[i] = findFlips(p.data)
			if errs[i] != nil {
				errs[i] = fmt.Errorf("%s のスキャンを読めません: %w", p.Name, errs[i])
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// insertPoint は SOI と、その直後に続く APPn セグメントを飛ばした位置を返す。
func insertPoint(data []byte) (int, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, fmt.Errorf("JPEG ではありません")
	}
	i := 2
	for i+4 <= len(data) && data[i] == 0xFF && data[i+1] >= 0xE0 && data[i+1] <= 0xEF {
		i += 2 + int(binary.BigEndian.Uint16(data[i+2:]))
	}
	if i > len(data) {
		return 0, fmt.Errorf("JPEG のセグメントが壊れています")
	}
	return i, nil
}

// derive は写真から RealMoji とプロフィール画像に使う正方形の小さな JPEG を作る。
func (s *Set) derive() error {
	var sources []*Photo
	for _, region := range []string{"asia", "europe", "america"} {
		sources = append(sources, s.byRegion[region]...)
	}
	if len(sources) == 0 {
		sources = s.photos
	}
	step := max(1, len(sources)/derivedCount)
	for i := 0; i < len(sources) && len(s.reactions) < derivedCount; i += step {
		img, err := jpegDecode(sources[i].data)
		if err != nil {
			return fmt.Errorf("%s を縮小できません: %w", sources[i].Name, err)
		}
		for _, target := range []struct {
			side int
			out  *[]jpeg
		}{{reactionSide, &s.reactions}, {profileSide, &s.profiles}} {
			data, err := encode(square(img, target.side))
			if err != nil {
				return err
			}
			insert, err := insertPoint(data)
			if err != nil {
				return err
			}
			f, err := findFlips(data)
			if err != nil {
				return err
			}
			*target.out = append(*target.out, jpeg{data: data, insert: insert, flips: f})
		}
	}
	return nil
}

func jpegDecode(data []byte) (image.Image, error) {
	return jpegStd.Decode(bytes.NewReader(data))
}

// square は中央を正方形に切り抜き、side x side に縮小する（面積平均）。
func square(img image.Image, side int) image.Image {
	b := img.Bounds()
	size := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-size)/2, b.Min.Y+(b.Dy()-size)/2
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		sy0, sy1 := y0+y*size/side, y0+(y+1)*size/side
		for x := range side {
			sx0, sx1 := x0+x*size/side, x0+(x+1)*size/side
			var r, g, bl, n uint64
			for sy := sy0; sy < max(sy1, sy0+1); sy++ {
				for sx := sx0; sx < max(sx1, sx0+1); sx++ {
					cr, cg, cb, _ := img.At(sx, sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			out.SetRGBA(x, y, color.RGBA{R: uint8(r / n >> 8), G: uint8(g / n >> 8), B: uint8(bl / n >> 8), A: 255})
		}
	}
	return out
}

func encode(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	if err := jpegStd.Encode(&buffer, img, &jpegStd.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// fetchTimeout はアーカイブのダウンロードにかける時間の上限。
const fetchTimeout = 2 * time.Minute

func fetch(ctx context.Context, source string) ([]byte, error) {
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		return os.ReadFile(source)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("写真のアーカイブを取得できません: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("写真のアーカイブを取得できません: ステータス %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}

func readAll(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func readJSON(f *zip.File, out any) error {
	data, err := readAll(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")), out)
}
