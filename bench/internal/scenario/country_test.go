package scenario

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// countriesSeen は path への要求の X-Country-Code を数える偽のアプリ。ほかの要求には status を返す。
func countriesSeen(t *testing.T, path string, status int, body string) (*httptest.Server, func() map[string]int) {
	var mu sync.Mutex
	countries := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if path == "" || req.URL.Path == path {
			mu.Lock()
			countries[req.Header.Get("X-Country-Code")]++
			mu.Unlock()
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return countries
	}
}

// 新しいユーザーの国は、リージョンのすべての国から選ぶ（asia は AU も）。
func TestNewcomerCountriesCoverRegion(t *testing.T) {
	for _, region := range geo.Regions {
		r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
		// 登録で止めて、負荷のループに入らないようにする。
		server, seen := countriesSeen(t, "/api/auth/register", http.StatusInternalServerError, "")
		r.api = client.New(server.URL)
		r.arriving[region] = 64
		for range 64 {
			r.runNewcomer(t.Context(), region)
		}
		countries := seen()
		for i := range geo.CountryCount(region) {
			if countries[geo.Country(region, i)] == 0 {
				t.Errorf("%s: newcomers never use %s: %v", region, geo.Country(region, i), countries)
			}
		}
	}
}

// 思い出を見返すユーザーを何回かに分けて増やしても、国は通し番号で散らす。
func TestReturningCountriesContinueAcrossGrowth(t *testing.T) {
	r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
	r.returningRatio = 8
	if n := len(r.initial.HistoryUsers(geo.Asia)); n < 4 {
		t.Skipf("only %d history users in asia", n)
	}
	// 最初のログインを BAN で止めて、ワーカーを終わらせる。
	server, seen := countriesSeen(t, "", http.StatusForbidden, `{"error":"ip_banned"}`)
	r.api = client.New(server.URL)
	for _, users := range []int{1, 9, 17, 25} {
		var wg sync.WaitGroup
		r.ensureReturning(t.Context(), &wg, geo.Asia, users)
		wg.Wait()
	}
	if countries := seen(); len(countries) != 4 {
		t.Fatalf("four workers added one by one used %v", countries)
	}
}
