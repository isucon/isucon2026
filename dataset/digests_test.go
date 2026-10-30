package dataset

import (
	"bytes"
	"testing"
)

// 表には初期データのすべての画像が入り、引いた値は画像の中身の SHA-256 と一致する。
// 別の設定の World では読めない。
func TestImageDigestsRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := Small()
	cfg.Days = 3
	world := Generate(cfg)
	var buf bytes.Buffer
	n, err := WriteImageDigests(&buf, world)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := ReadImageDigests(bytes.NewReader(buf.Bytes()), world)
	if err != nil {
		t.Fatal(err)
	}
	var images []*Image
	for _, u := range world.Users {
		if u.ProfileImage != nil {
			images = append(images, u.ProfileImage)
		}
	}
	for day := range cfg.Days {
		for _, p := range world.PostsOn(day) {
			images = append(images, p.Image)
			for _, r := range p.RealMojis {
				images = append(images, r.Image)
			}
		}
	}
	if n != len(images) || digests.Len() != n {
		t.Fatalf("wrote %d digests, read %d, want %d", n, digests.Len(), len(images))
	}
	for i, img := range images {
		if i%97 != 0 {
			continue
		}
		got, ok := digests.Lookup(img.ID)
		if want := ImageDigest(world.ImageBytes(img)); !ok || got != want {
			t.Fatalf("Lookup(%s) = %x, %v; want %x", img.ID, got, ok, want)
		}
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000000000", "not-a-uuid", ""} {
		if _, ok := digests.Lookup(id); ok {
			t.Errorf("Lookup(%q) found a digest", id)
		}
	}

	other := cfg
	other.Seed++
	if _, err := ReadImageDigests(bytes.NewReader(buf.Bytes()), Generate(other)); err == nil {
		t.Error("read a table made with another seed")
	}
	if _, err := ReadImageDigests(bytes.NewReader(buf.Bytes()[:buf.Len()-1]), world); err == nil {
		t.Error("read a truncated table")
	}
	if _, err := ReadImageDigests(bytes.NewReader(append(bytes.Clone(buf.Bytes()), 0)), world); err == nil {
		t.Error("read a table with trailing bytes")
	}
	// 画像の作り方が変わった版の表は、設定が同じでもすべての値が違う。
	stale := bytes.Clone(buf.Bytes())
	for i := len(stale) - n*imageRecordSize + imageIDSize; i < len(stale); i += imageRecordSize {
		stale[i] ^= 1
	}
	if _, err := ReadImageDigests(bytes.NewReader(stale), world); err == nil {
		t.Error("read a table whose digests differ from the images")
	}
}
