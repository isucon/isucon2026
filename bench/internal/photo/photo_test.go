package photo

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"image"
	jpegStd "image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// writeArchive は manifest.json と JPEG を 1 枚ずつ持つ地域のセットのアーカイブを作る。
func writeArchive(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	type entry struct {
		Filename      string `json:"filename"`
		SettingRegion string `json:"setting_region"`
		TitleJA       string `json:"title_ja"`
	}
	entries := []entry{{"001_a.jpg", "アジア", "縁側のベンチ"}, {"002_e.jpg", "ヨーロッパ", "広場のベンチ"}, {"003_u.jpg", "アメリカ", "球場の席"}}
	manifest, _ := json.Marshal(entries)
	f, _ := w.Create("set/manifest.json")
	_, _ = f.Write(manifest)
	for i, e := range entries {
		var img bytes.Buffer
		_ = jpegStd.Encode(&img, image.NewRGBA(image.Rect(0, 0, 400+i, 300)), nil)
		f, _ := w.Create("set/jpeg/" + e.Filename)
		_, _ = f.Write(img.Bytes())
	}
	f, _ = w.Create("set/contact_sheets/contact.jpg")
	_, _ = f.Write([]byte("not a photo"))
	_ = w.Close()
	path := filepath.Join(t.TempDir(), "photos.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAndMark(t *testing.T) {
	set, err := Load(t.Context(), writeArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	if set.Count() != 3 {
		t.Fatalf("count=%d, want 3", set.Count())
	}
	// 真っ黒な写真には反転できる係数がないので、COM セグメントの印だけになる。
	if set.Markable() != 0 {
		t.Fatalf("markable=%d, want 0 for flat images", set.Markable())
	}
	a, caption := set.Post("europe", 1000)
	b, _ := set.Post("europe", 1001)
	if caption != "広場のベンチ" {
		t.Fatalf("caption=%q, want the europe photo's title", caption)
	}
	if sha256.Sum256(a) == sha256.Sum256(b) {
		t.Fatal("two posts got the same bytes")
	}
	for _, data := range [][]byte{a, set.RealMoji(7), set.Profile(7)} {
		img, err := jpegStd.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("marked image does not decode: %v", err)
		}
		if img.Bounds().Dx() > 512 && len(data) != len(a) {
			t.Fatalf("derived image is too large: %v", img.Bounds())
		}
	}
	again, _ := set.Post("europe", 1000)
	if !bytes.Equal(a, again) {
		t.Fatal("the same key gave different bytes")
	}
}

func TestGenerated(t *testing.T) {
	set := Generated()
	a, _ := set.Post("asia", 1)
	b, _ := set.Post("asia", 2)
	if bytes.Equal(a, b) || !set.IsGenerated() {
		t.Fatal("generated posts should differ")
	}
}

// TestLoadArchive は ISUXBENCH_IMAGES のアーカイブを実際に読む（指定がなければ飛ばす）。
func TestLoadArchive(t *testing.T) {
	source := os.Getenv("ISUXBENCH_IMAGES")
	if source == "" {
		t.Skip("ISUXBENCH_IMAGES is not set")
	}
	set, err := Load(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("photos=%d asia=%d europe=%d america=%d novelty=%d reactions=%d profiles=%d",
		set.Count(), len(set.byRegion["asia"]), len(set.byRegion["europe"]), len(set.byRegion["america"]), len(set.novelty), len(set.reactions), len(set.profiles))
	data, caption := set.Post("asia", 123456)
	t.Logf("post bytes=%d caption=%q reaction=%d profile=%d", len(data), caption, len(set.RealMoji(1)), len(set.Profile(1)))
	if set.Markable() != set.Count() {
		t.Errorf("markable=%d, want every photo (%d)", set.Markable(), set.Count())
	}
	for _, p := range set.photos {
		windows := 0
		for _, candidates := range p.flips {
			if len(candidates) > 0 {
				windows++
			}
		}
		if windows < len(p.flips)/2 {
			t.Errorf("%s: only %d of %d windows can be flipped", p.Name, windows, len(p.flips))
		}
	}
	other, _ := set.Post("asia", 123456+1000*uint64(len(set.byRegion["asia"])))
	a, b := decodeRGBA(t, data), decodeRGBA(t, other)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("two posts of the same photo have the same pixels")
	}
}
