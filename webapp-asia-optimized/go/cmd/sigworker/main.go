// sigworker は初期データの画像の署名を exp ごとに事前計算して isu01 に送る。
//
//	sigworker dump <out>                    DB から画像 ID と署名鍵の一覧を作る
//	sigworker work <keys> <part> <parts>    一覧のうち part 番目を担当して計算し続ける
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"time"

	"example.com/isureal-company/isureal"
	"github.com/jmoiron/sqlx"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const target = "http://192.168.97.10:8090/sigs"

func main() {
	switch os.Args[1] {
	case "dump":
		dump(os.Args[2])
	case "work":
		part, _ := strconv.Atoi(os.Args[3])
		parts, _ := strconv.Atoi(os.Args[4])
		work(os.Args[2], part, parts)
	}
}

func dump(out string) {
	db := sqlx.MustOpen("pgx", isureal.ConfigFromEnv().DatabaseDSN())
	rows, err := db.Query(`SELECT i.id, u.secret_key_hash FROM image_objects i JOIN users u ON u.id = i.owner_user_id
		WHERE i.created_at <= (SELECT created_at FROM initial_data_boundary)`)
	if err != nil {
		log.Fatal(err)
	}
	var recs [][]byte
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			log.Fatal(err)
		}
		rec := make([]byte, 48)
		if _, err := hex.Decode(rec[:16], []byte(id[0:8]+id[9:13]+id[14:18]+id[19:23]+id[24:36])); err != nil {
			log.Fatal(err)
		}
		if _, err := hex.Decode(rec[16:], []byte(key)); err != nil {
			log.Fatal(err)
		}
		recs = append(recs, rec)
	}
	slices.SortFunc(recs, func(a, b []byte) int { return bytes.Compare(a[:16], b[:16]) })
	if err := os.WriteFile(out, bytes.Join(recs, nil), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Println("records:", len(recs))
}

func work(keys string, part, parts int) {
	all, err := os.ReadFile(keys)
	if err != nil {
		log.Fatal(err)
	}
	var mine []byte
	n := len(all) / 48
	for i := part; i < n; i += parts {
		mine = append(mine, all[i*48:(i+1)*48]...)
	}
	log.Printf("part %d/%d: %d records", part, parts, len(mine)/48)
	built := map[int64][]byte{}
	for {
		cur := time.Now().Add(24*time.Hour).Unix() / isureal.ImageExpQuantum * isureal.ImageExpQuantum
		for e := range built {
			if e < cur {
				delete(built, e)
				os.Remove(fmt.Sprintf("/home/isucon/sigcache/%d-%d-%d.bin", e, part, parts))
			}
		}
		for _, exp := range []int64{cur, cur + isureal.ImageExpQuantum} {
			url := fmt.Sprintf("%s?exp=%d&part=%d", target, exp, part)
			if resp, err := http.Get(url); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					continue
				}
			}
			table, ok := built[exp]
			if !ok {
				// 再起動に備えて計算済みの表はディスクにも置く
				path := fmt.Sprintf("/home/isucon/sigcache/%d-%d-%d.bin", exp, part, parts)
				if data, err := os.ReadFile(path); err == nil && len(data) == len(mine) {
					table = data
				} else {
					start := time.Now()
					table = isureal.BuildSignatureTable(mine, exp, runtime.GOMAXPROCS(0))
					log.Printf("built exp=%d in %s", exp, time.Since(start))
					os.MkdirAll("/home/isucon/sigcache", 0o755)
					os.WriteFile(path+".tmp", table, 0o600)
					os.Rename(path+".tmp", path)
				}
				built[exp] = table
			}
			req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(table))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("push exp=%d: %v", exp, err)
				continue
			}
			resp.Body.Close()
			log.Printf("pushed exp=%d: %d", exp, resp.StatusCode)
		}
		time.Sleep(5 * time.Second)
	}
}
