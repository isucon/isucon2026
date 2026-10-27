package isureal

import (
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// 初期データの画像の署名を isu02/isu03 の sigworker が exp ごとに事前計算して送ってくる。
// レコードは 16 バイトの画像 ID（UUID）と 32 バイトの署名で、画像 ID の昇順に並んでいる。
const sigRecordSize = 16 + 32

type sigTablePart struct {
	data []byte
}

func (p *sigTablePart) lookup(id [16]byte) ([]byte, bool) {
	n := len(p.data) / sigRecordSize
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		rec := p.data[mid*sigRecordSize : mid*sigRecordSize+16]
		c := compare16(rec, id[:])
		switch {
		case c == 0:
			return p.data[mid*sigRecordSize+16 : (mid+1)*sigRecordSize], true
		case c < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return nil, false
}

func compare16(a, b []byte) int {
	x, y := binary.BigEndian.Uint64(a), binary.BigEndian.Uint64(b)
	if x != y {
		if x < y {
			return -1
		}
		return 1
	}
	x, y = binary.BigEndian.Uint64(a[8:]), binary.BigEndian.Uint64(b[8:])
	if x != y {
		if x < y {
			return -1
		}
		return 1
	}
	return 0
}

var (
	sigTablesMu sync.RWMutex
	sigTables   = map[int64]map[int]*sigTablePart{} // exp -> part -> table
)

func parseUUID(s string) ([16]byte, bool) {
	var id [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return id, false
	}
	var buf [32]byte
	n := 0
	for i := 0; i < 36; i++ {
		if s[i] == '-' {
			continue
		}
		buf[n] = s[i]
		n++
	}
	if _, err := hex.Decode(id[:], buf[:]); err != nil {
		return id, false
	}
	return id, true
}

func precomputedSignature(imageID string, exp int64) ([]byte, bool) {
	sigTablesMu.RLock()
	parts := sigTables[exp]
	sigTablesMu.RUnlock()
	if len(parts) == 0 {
		return nil, false
	}
	id, ok := parseUUID(imageID)
	if !ok {
		return nil, false
	}
	for _, p := range parts {
		if sig, ok := p.lookup(id); ok {
			return sig, true
		}
	}
	return nil, false
}

// sigworker からの受け口（:8090）
func sigTableHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sigs", func(w http.ResponseWriter, r *http.Request) {
		exp, err1 := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
		part, err2 := strconv.Atoi(r.URL.Query().Get("part"))
		if err1 != nil || err2 != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			sigTablesMu.RLock()
			_, ok := sigTables[exp][part]
			sigTablesMu.RUnlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			// 表は Go のヒープの外（mmap）に置き、GC の対象と GOMEMLIMIT の計算から外す
			n := r.ContentLength
			if n <= 0 || n%sigRecordSize != 0 {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			data, err := syscall.Mmap(-1, 0, int(n), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if _, err := io.ReadFull(r.Body, data); err != nil {
				syscall.Munmap(data)
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			now := time.Now().Add(imageURLLifetime).Unix() / imageExpQuantum * imageExpQuantum
			sigTablesMu.Lock()
			for e, parts := range sigTables {
				if e < now {
					delete(sigTables, e)
					for _, p := range parts {
						releaseSigTable(p)
					}
				}
			}
			if old, ok := sigTables[exp][part]; ok {
				releaseSigTable(old)
			}
			if sigTables[exp] == nil {
				sigTables[exp] = map[int]*sigTablePart{}
			}
			sigTables[exp][part] = &sigTablePart{data: data}
			sigTablesMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

// 参照中の検索が終わるまで待ってから解放する
func releaseSigTable(p *sigTablePart) {
	time.AfterFunc(time.Minute, func() { syscall.Munmap(p.data) })
}

// sigworker 用: 画像 ID と署名鍵の一覧から exp の署名表を作る（records は ID 昇順の 16+32 バイトのレコード）
func BuildSignatureTable(records []byte, exp int64, workers int) []byte {
	n := len(records) / sigRecordSize
	out := make([]byte, n*sigRecordSize)
	var wg sync.WaitGroup
	prefix := strconv.FormatInt(exp, 10) + ":"
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			idHex := make([]byte, 36)
			for i := w; i < n; i += workers {
				rec := records[i*sigRecordSize : (i+1)*sigRecordSize]
				formatUUID(idHex, rec[:16])
				sig := fastImageSignature(rec[16:48], []byte(prefix+string(idHex)))
				copy(out[i*sigRecordSize:], rec[:16])
				copy(out[i*sigRecordSize+16:], sig)
			}
		}(w)
	}
	wg.Wait()
	return out
}

func formatUUID(dst []byte, id []byte) {
	hex.Encode(dst[0:8], id[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], id[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], id[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], id[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], id[10:16])
}

// ImageExpQuantum は sigworker と exp の単位を合わせるために公開する
const ImageExpQuantum = imageExpQuantum
