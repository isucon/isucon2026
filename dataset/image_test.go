package dataset

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/dataset/internal/resize"
)

// TestPhotoCatalog は写真の一覧と写真のファイル、コメントが揃っていることを確かめる。
func TestPhotoCatalog(t *testing.T) {
	files, err := fs.Glob(photoFiles, "photos/*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	chairs := slices.Concat(plainChairs, noveltyChairs)
	if len(files) != len(chairs) {
		t.Fatalf("%d photos but %d rows in catalog.tsv", len(files), len(chairs))
	}
	for _, c := range chairs {
		if !slices.Contains(files, "photos/"+c.file) {
			t.Fatalf("%s is not in photos/", c.file)
		}
		img := c.decoded()
		if b := img.Bounds(); img.SubsampleRatio != image.YCbCrSubsampleRatio420 || min(b.Dx(), b.Dy()) < Large().PostImageSize {
			t.Fatalf("%s: %v %v is too small or not 4:2:0", c.file, b, img.SubsampleRatio)
		}
		for _, code := range languageCodes {
			if len(themeComments[c.theme][code]) == 0 {
				t.Fatalf("%s: no %s comments for theme %q", c.file, code, c.theme)
			}
		}
	}
	for _, region := range Regions {
		if len(regionChairs[region]) < 10 {
			t.Fatalf("%s has only %d photos", region, len(regionChairs[region]))
		}
	}
	if len(noveltyChairs) == 0 {
		t.Fatal("no novelty photos")
	}
	for theme, byLanguage := range themeComments {
		if !slices.ContainsFunc(chairs, func(c *chair) bool { return c.theme == theme }) {
			t.Fatalf("comments.tsv has unused theme %q", theme)
		}
		for code, comments := range byLanguage {
			for _, comment := range comments {
				if strings.TrimSpace(comment) != comment {
					t.Fatalf("%s %s: comment %q has surrounding spaces", theme, code, comment)
				}
			}
		}
	}
}

// TestImagesUnique は、同じ写真の同じ位置を切り抜いた画像どうしでも、画素のデータまで
// 一致しないことを確かめる。明るさと色味のずらし方は 833 通りしかないので、2,000 枚では
// 必ず重なり、輝度の揺らぎだけで区別できなければならない。履歴の大きさは画像の重複を
// 除けないことに頼っている。
func TestImagesUnique(t *testing.T) {
	size := Large().RealMojiImageSize
	src := plainChairs[0].decoded()
	crop := resize.Crop(src, 0, 0, float64(src.Rect.Dy()), float64(src.Rect.Dy()), size, size, false)
	seen := map[[32]byte]string{}
	for i := range 2000 {
		id := fmt.Sprintf("unique-%d", i)
		picture := *crop
		picture.Y, picture.Cb, picture.Cr = slices.Clone(crop.Y), slices.Clone(crop.Cb), slices.Clone(crop.Cr)
		data := finishPhoto(&picture, newRand(1, "image-"+id))
		decoded, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(decoded.(*image.YCbCr).Y)
		if other, ok := seen[sum]; ok {
			t.Fatalf("%s and %s have the same pixels", id, other)
		}
		seen[sum] = id
	}
}

// TestImageSizes は画像の大きさの平均が履歴の規模の見積もり（Large の投稿約 15 KB、RealMoji
// 約 3.5 KB）から大きく外れないことを確かめる。
func TestImageSizes(t *testing.T) {
	cfg := Large()
	cfg.Users, cfg.Days = 50, 1
	world := Generate(cfg)
	for _, c := range []struct {
		kind ImageKind
		want int
	}{{ImagePost, 15000}, {ImageRealMoji, 3500}, {ImageProfile, 2400}} {
		total, n := 0, 400
		for i := range n {
			total += len(world.ImageBytes(&Image{ID: fmt.Sprintf("size-%d", i), OwnerID: world.Users[i%len(world.Users)].ID, Kind: c.kind}))
		}
		average := total / n
		t.Logf("%s: %d bytes on average", c.kind, average)
		if average < c.want*7/10 || average > c.want*13/10 {
			t.Errorf("%s: average %d bytes, want about %d", c.kind, average, c.want)
		}
	}
}

func BenchmarkImageBytes(b *testing.B) {
	cfg := Large()
	cfg.Users, cfg.Days = 20, 1
	world := Generate(cfg)
	for _, kind := range []ImageKind{ImagePost, ImageRealMoji, ImageProfile} {
		b.Run(string(kind), func(b *testing.B) {
			for i := range b.N {
				world.ImageBytes(&Image{ID: fmt.Sprintf("bench-%d", i), OwnerID: world.Users[0].ID, Kind: kind})
			}
		})
	}
}
