package isureal

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v5"
)

// リージョンごとのポートで待ち受ける。europe/america のサーバーの nginx はそれぞれのポートへ転送してくる。
// asia はルーターから直接 :80 で受ける（静的ファイルもここで返す）。
var regionPorts = map[string]string{":80": "asia", ":8081": "europe", ":8082": "america"}

const publicDir = "/home/isucon/webapp/public"

func regionHandler(e *echo.Echo, region string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if region != "" {
			r.Header.Set("X-Isureal-Region", region)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			serveStatic(w, r)
			return
		}
		// クライアントの切断で DB へのクエリを中断すると pgx の接続が壊れ、後続のリクエストが
		// driver: bad connection で失敗するので、キャンセルを伝えない（SSE の通知だけは切断を検知する）
		if r.URL.Path != "/api/notifications" {
			r = r.WithContext(context.WithoutCancel(r.Context()))
		}
		e.ServeHTTP(w, r)
	})
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(publicDir, filepath.Clean("/"+r.URL.Path))
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		path = filepath.Join(publicDir, "index.html")
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func ListenAll(e *echo.Echo) error {
	errs := make(chan error, len(regionPorts)+1)
	for addr, region := range regionPorts {
		go func() {
			errs <- http.ListenAndServe(addr, regionHandler(e, region))
		}()
	}
	// sigworker から事前計算した署名表を受け取る
	go func() {
		errs <- http.ListenAndServe(":8090", sigTableHandler())
	}()
	// 従来の :8080（ヘッダーまたは設定のリージョンを使う）
	go func() {
		errs <- http.ListenAndServe(":8080", regionHandler(e, ""))
	}()
	return <-errs
}
