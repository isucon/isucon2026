// datagen は ISUReal の初期データ（webapp/sql/002-seed.sql）を生成する。
//
// データは dataset モジュールが決定的に作るので、DB は不要。bench も同じ dataset から
// 同じデータを再計算する。
//
//	cd webapp/datagen && go run .
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/isucon/isucon2026/dataset"
)

func main() {
	out := flag.String("out", filepath.Join("..", "sql", "002-seed.sql"), "path to write the seed SQL (- for stdout)")
	scale := flag.String("scale", "small", "data scale: small (development) or large (production)")
	flag.Parse()
	cfg, ok := map[string]func() dataset.Config{"small": dataset.Small, "large": dataset.Large}[*scale]
	if !ok {
		fmt.Fprintln(os.Stderr, "error: unknown scale", *scale)
		os.Exit(2)
	}
	if err := run(*out, cfg()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run は一時ファイルに書いてから置き換えるので、途中で失敗しても古い seed は壊れない。
// out が - なら標準出力に書く。大きな規模を、ファイルを経ずに psql へ流すのに使う。
func run(out string, cfg dataset.Config) error {
	world := dataset.Generate(cfg)
	if out == "-" {
		stats, err := writeSeed(os.Stdout, world)
		if err == nil {
			printStats("stdout", stats)
		}
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".002-seed-*.sql")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	stats, err := writeSeed(tmp, world)
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
	printStats(out, stats)
	return nil
}

func printStats(out string, stats seedStats) {
	fmt.Fprintf(os.Stderr, "wrote %s: %d users, %d posts, %d realmojis, %d comments, %d images (%.1f MB)\n",
		out, stats.users, stats.posts, stats.realMojis, stats.comments, stats.images, float64(stats.imageBytes)/1e6)
}
