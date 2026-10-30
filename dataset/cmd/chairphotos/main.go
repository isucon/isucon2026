// chairphotos は椅子の写真のアーカイブ（bench が投稿に使う merged.zip）から、初期データの
// 画像の元にする縮小版を dataset/photos/ に書き出す。
//
//	cd dataset && go run ./cmd/chairphotos -archive ~/merged.zip
//
// 写真の一覧（photos/catalog.tsv）の翻訳とテーマは手で書いているので、このツールは画像だけを
// 書き換える。アーカイブの写真が増えたら catalog.tsv にも行を足す。
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/isucon/isucon2026/dataset/internal/resize"
)

// short は縮小版の短い辺の画素数。投稿の画像（Large で 320 px）を短い辺の 8 割以上から
// 切り抜くので、拡大しすぎない大きさにする。
const short = 360

func main() {
	archive := flag.String("archive", "", "path to the chair photo archive (merged.zip)")
	out := flag.String("out", "photos", "directory to write the resized photos")
	flag.Parse()
	if err := run(*archive, *out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(archive, out string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	files := map[string]*zip.File{}
	var names []string
	for _, f := range r.File {
		name := path.Base(f.Name)
		switch {
		case name == "manifest.json":
			var entries []struct {
				Filename string `json:"filename"`
			}
			if err := readJSON(f, &entries); err != nil {
				return fmt.Errorf("%s: %w", f.Name, err)
			}
			for _, e := range entries {
				names = append(names, e.Filename)
			}
		case strings.HasSuffix(name, ".jpg") && !strings.Contains(f.Name, "contact"):
			files[name] = f
		}
	}
	var total int
	for _, name := range names {
		f, ok := files[name]
		if !ok {
			return fmt.Errorf("%s is in a manifest but not in the archive", name)
		}
		data, err := shrink(f)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return err
		}
		total += len(data)
	}
	fmt.Fprintf(os.Stderr, "wrote %d photos (%.1f MB) to %s\n", len(names), float64(total)/1e6, out)
	return nil
}

func shrink(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	img, err := jpeg.Decode(rc)
	if err != nil {
		return nil, err
	}
	src, ok := img.(*image.YCbCr)
	if !ok {
		return nil, fmt.Errorf("not a YCbCr JPEG (%T)", img)
	}
	b := src.Bounds()
	w, h := b.Dx()*short/min(b.Dx(), b.Dy()), b.Dy()*short/min(b.Dx(), b.Dy())
	small := resize.Crop(src, 0, 0, float64(b.Dx()), float64(b.Dy()), w, h, false)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readJSON(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
