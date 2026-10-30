// imagedigests は初期データの画像の SHA-256 の表を作る。bench は -image-digests でこれを読み、
// 初期データの画像を描き直さずに確かめる。すべての画像を描くので、Large では CPU 時間で
// 20 分ほどかかる（CPU の数だけ並行に描く）。
//
//	cd bench && go run ./cmd/imagedigests -scale large -out ../var/image-digests/large.bin
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isucon/isucon2026/dataset"
)

func main() {
	out := flag.String("out", "", "path to write the table")
	scale := flag.String("scale", "small", "data scale: small or large (must match datagen -scale and the benchmark's -scale)")
	flag.Parse()
	cfg, ok := map[string]func() dataset.Config{"small": dataset.Small, "large": dataset.Large}[*scale]
	if !ok || *out == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*out, cfg()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run は一時ファイルに書いてから置き換えるので、途中で失敗しても古い表は壊れない。
func run(out string, cfg dataset.Config) error {
	started := time.Now()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".image-digests-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := dataset.WriteImageDigests(tmp, dataset.Generate(cfg))
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d images in %s\n", out, n, time.Since(started).Round(time.Second))
	return nil
}
