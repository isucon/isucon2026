package main

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/isucon/isucon2026/dataset"
)

// reset.sql は、名前が初期データの最後の作成時刻の月（image_objects_YYYYMM）より後のパーティションを
// TRUNCATE する。初期データの画像がそこに入ると初期化で消えてしまい、走行の画像が TRUNCATE しない
// パーティションに入ると DELETE で消すことになって初期化が遅くなる。どちらも起きないことを、
// どの規模についても確かめる。
func TestInitialDataPartitions(t *testing.T) {
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	type partition struct {
		name string
		from time.Time
	}
	var partitions []partition
	re := regexp.MustCompile(`CREATE TABLE (image_objects_\w+) PARTITION OF image_objects FOR VALUES FROM \('([^']+)'\)`)
	for _, m := range re.FindAllStringSubmatch(string(schema), -1) {
		from, err := time.Parse("2006-01-02 15:04:05-07", m[2])
		if err != nil {
			t.Fatal(err)
		}
		partitions = append(partitions, partition{m[1], from})
	}
	if len(partitions) == 0 {
		t.Fatal("image_objects のパーティションが見つからない")
	}

	for name, cfg := range map[string]dataset.Config{"small": dataset.Small(), "large": dataset.Large()} {
		// 投稿、RealMoji、コメントとその画像は、投稿期間が終わるまでに作られる。
		var last time.Time
		for _, window := range dataset.Generate(cfg).Windows {
			if window.EndedAt.After(last) {
				last = window.EndedAt
			}
		}
		kept := "image_objects_" + last.UTC().Format("200601")
		var next time.Time
		for _, p := range partitions {
			if p.name <= kept {
				continue
			}
			if !p.from.After(last) {
				t.Errorf("%s: 初期データの最後の作成時刻 %s が、初期化で TRUNCATE する %s（%s から）に入る", name, last, p.name, p.from)
			}
			if next.IsZero() || p.from.Before(next) {
				next = p.from
			}
		}
		if gap := next.Sub(last); gap > 24*time.Hour {
			t.Errorf("%s: 初期データの最後の作成時刻 %s から、TRUNCATE するパーティションの始まり %s まで %s あり、その間の走行の画像は DELETE で消すことになる", name, last, next, gap)
		}
	}
}
