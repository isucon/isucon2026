package dataset

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"sort"
	"sync"
)

// ImageDigests は初期データの画像ごとの SHA-256 の表。bench は初期データの画像を確かめるとき、
// Router に画像の SHA-256 を返させてこの表と比べる。画像を描き直さずに済む（320 px の画像
// 1 枚を椅子の写真から描くのに約 2.5 ms かかり、bench の CPU の大半を占めていた）。
//
// 表は WriteImageDigests で前もって作ってファイルに置き、bench は ReadImageDigests で読む。
// 形式は imageDigestsMagic、World の設定の SHA-256（32 バイト）、件数（uint64、ビッグエンディアン）
// のあとに、画像 ID（UUID の 16 バイト）と中身の SHA-256 の先頭 ImageDigestSize バイトの組を
// ID の順に並べたもの。
type ImageDigests struct {
	records []byte
}

// ImageDigestSize は表に入れる SHA-256 の長さ。中身を合わせるには SHA-256 の第二原像が要り、
// 128 ビットで足りる。
const ImageDigestSize = 16

const (
	imageDigestsMagic = "isureal-image-digests-v1\n"
	imageIDSize       = 16
	imageRecordSize   = imageIDSize + ImageDigestSize
)

// ImageDigest は data の SHA-256 の先頭 ImageDigestSize バイト。表にはこれを入れる。
func ImageDigest(data []byte) [ImageDigestSize]byte {
	sum := sha256.Sum256(data)
	return [ImageDigestSize]byte(sum[:ImageDigestSize])
}

// configDigest は表を作った World の設定を表す。設定が違えば画像も違うので、ほかの設定の
// 表は読まない。
func (w *World) configDigest() ([sha256.Size]byte, error) {
	data, err := json.Marshal(w.Config)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

// parseUUID は 8-4-4-4-12 の形の ID を 16 バイトにする。
func parseUUID(id string) (out [imageIDSize]byte, ok bool) {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return out, false
	}
	var digits [32]byte
	n := 0
	for i := range len(id) {
		if i != 8 && i != 13 && i != 18 && i != 23 {
			digits[n] = id[i]
			n++
		}
	}
	if _, err := hex.Decode(out[:], digits[:]); err != nil {
		return out, false
	}
	return out, true
}

// WriteImageDigests は w の初期データのすべての画像（プロフィール、投稿、RealMoji）を描いて
// 表を作り、out に書く。画像の生成に CPU の数だけ並行に使う。書いた件数を返す。
func WriteImageDigests(out io.Writer, w *World) (int, error) {
	config, err := w.configDigest()
	if err != nil {
		return 0, err
	}
	var records [][imageRecordSize]byte
	add := func(images []*Image) error {
		start := len(records)
		records = slices.Grow(records, len(images))[:start+len(images)]
		for i, img := range images {
			id, ok := parseUUID(img.ID)
			if !ok {
				return fmt.Errorf("image ID %q is not a UUID", img.ID)
			}
			copy(records[start+i][:imageIDSize], id[:])
		}
		next := make(chan int)
		var wg sync.WaitGroup
		for range runtime.NumCPU() {
			wg.Go(func() {
				for i := range next {
					sum := ImageDigest(w.ImageBytes(images[i]))
					copy(records[start+i][imageIDSize:], sum[:])
				}
			})
		}
		for i := range images {
			next <- i
		}
		close(next)
		wg.Wait()
		return nil
	}
	var profiles []*Image
	for _, u := range w.Users {
		if u.ProfileImage != nil {
			profiles = append(profiles, u.ProfileImage)
		}
	}
	if err := add(profiles); err != nil {
		return 0, err
	}
	for day := range w.Config.Days {
		var images []*Image
		for _, p := range w.PostsOn(day) {
			images = append(images, p.Image)
			for _, r := range p.RealMojis {
				images = append(images, r.Image)
			}
		}
		if err := add(images); err != nil {
			return 0, err
		}
	}
	slices.SortFunc(records, func(a, b [imageRecordSize]byte) int { return bytes.Compare(a[:imageIDSize], b[:imageIDSize]) })
	for i := 1; i < len(records); i++ {
		if bytes.Equal(records[i-1][:imageIDSize], records[i][:imageIDSize]) {
			return 0, fmt.Errorf("image ID %x appears twice", records[i][:imageIDSize])
		}
	}
	bw := bufio.NewWriter(out)
	bw.WriteString(imageDigestsMagic)
	bw.Write(config[:])
	binary.Write(bw, binary.BigEndian, uint64(len(records)))
	for i := range records {
		bw.Write(records[i][:])
	}
	return len(records), bw.Flush()
}

// ReadImageDigests は WriteImageDigests が書いた表を読む。w と違う設定で作った表は誤りとする。
func ReadImageDigests(r io.Reader, w *World) (*ImageDigests, error) {
	config, err := w.configDigest()
	if err != nil {
		return nil, err
	}
	head := make([]byte, len(imageDigestsMagic)+len(config)+8)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("画像のダイジェストの表を読めません: %w", err)
	}
	if string(head[:len(imageDigestsMagic)]) != imageDigestsMagic {
		return nil, errors.New("画像のダイジェストの表ではありません")
	}
	if !bytes.Equal(head[len(imageDigestsMagic):len(imageDigestsMagic)+len(config)], config[:]) {
		return nil, errors.New("画像のダイジェストの表が別の初期データ（規模か seed が違う）のものです")
	}
	count := binary.BigEndian.Uint64(head[len(head)-8:])
	if count > 1<<28 {
		return nil, fmt.Errorf("画像のダイジェストの表の件数 %d が大きすぎます", count)
	}
	d := &ImageDigests{records: make([]byte, count*imageRecordSize)}
	if _, err := io.ReadFull(r, d.records); err != nil {
		return nil, fmt.Errorf("画像のダイジェストの表を読めません: %w", err)
	}
	if n, _ := r.Read(make([]byte, 1)); n != 0 {
		return nil, errors.New("画像のダイジェストの表の後ろに余分なデータがあります")
	}
	for i := 1; i < int(count); i++ {
		if bytes.Compare(d.id(i-1), d.id(i)) >= 0 {
			return nil, errors.New("画像のダイジェストの表が ID の順に並んでいません")
		}
	}
	return d, nil
}

func (d *ImageDigests) id(i int) []byte {
	return d.records[i*imageRecordSize : i*imageRecordSize+imageIDSize]
}

// Len は表の件数。
func (d *ImageDigests) Len() int { return len(d.records) / imageRecordSize }

// Lookup は画像 id の中身の SHA-256 の先頭 ImageDigestSize バイトを返す。
func (d *ImageDigests) Lookup(id string) (digest [ImageDigestSize]byte, ok bool) {
	key, ok := parseUUID(id)
	if !ok {
		return digest, false
	}
	n := d.Len()
	i := sort.Search(n, func(i int) bool { return bytes.Compare(d.id(i), key[:]) >= 0 })
	if i == n || !bytes.Equal(d.id(i), key[:]) {
		return digest, false
	}
	copy(digest[:], d.records[i*imageRecordSize+imageIDSize:])
	return digest, true
}
