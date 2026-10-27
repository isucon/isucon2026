package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

// ルートごとの処理時間を集計する（計測用。127.0.0.1:6060/debug/routes で参照）
type routeStat struct {
	Count int64
	Sum   time.Duration
	Max   time.Duration
}

// 秒ごとの集計（通知のロングポーリングを除く）
type secondStat struct {
	Count, Slow, Errors int64
	Sum, Max            time.Duration
	SlowRoutes          map[string]int
}

var (
	routeStatsMu sync.Mutex
	routeStats   = map[string]*routeStat{}
	secondStats  = map[int64]*secondStat{}
)

func RouteStatsMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			start := time.Now()
			err := next(c)
			elapsed := time.Since(start)
			key := c.Request().Method + " " + c.RouteInfo().Path
			routeStatsMu.Lock()
			s := routeStats[key]
			if s == nil {
				s = &routeStat{}
				routeStats[key] = s
			}
			s.Count++
			s.Sum += elapsed
			s.Max = max(s.Max, elapsed)
			if c.RouteInfo().Path != "/api/notifications" {
				sec := start.Unix()
				t := secondStats[sec]
				if t == nil {
					t = &secondStat{}
					secondStats[sec] = t
				}
				t.Count++
				t.Sum += elapsed
				t.Max = max(t.Max, elapsed)
				if elapsed > 500*time.Millisecond {
					t.Slow++
					if t.SlowRoutes == nil {
						t.SlowRoutes = map[string]int{}
					}
					t.SlowRoutes[key]++
				}
				if err != nil {
					t.Errors++
				}
			}
			routeStatsMu.Unlock()
			return err
		}
	}
}

func init() {
	http.HandleFunc("/debug/routes", func(w http.ResponseWriter, r *http.Request) {
		routeStatsMu.Lock()
		type row struct {
			Route string
			Count int64
			AvgMs float64
			MaxMs float64
			SumS  float64
		}
		var rows []row
		for k, s := range routeStats {
			rows = append(rows, row{k, s.Count, float64(s.Sum.Microseconds()) / 1000 / float64(s.Count), float64(s.Max.Microseconds()) / 1000, s.Sum.Seconds()})
		}
		if r.URL.Query().Get("reset") != "" {
			routeStats = map[string]*routeStat{}
			secondStats = map[int64]*secondStat{}
		}
		routeStatsMu.Unlock()
		sort.Slice(rows, func(i, j int) bool { return rows[i].SumS > rows[j].SumS })
		enc := json.NewEncoder(w)
		for _, r := range rows {
			enc.Encode(r)
		}
	})
}

func init() {
	http.HandleFunc("/debug/seconds", func(w http.ResponseWriter, r *http.Request) {
		routeStatsMu.Lock()
		secs := make([]int64, 0, len(secondStats))
		for k := range secondStats {
			secs = append(secs, k)
		}
		sort.Slice(secs, func(i, j int) bool { return secs[i] < secs[j] })
		for _, k := range secs {
			t := secondStats[k]
			fmt.Fprintf(w, "%s count=%d avg=%.1fms max=%.0fms slow=%d err=%d\n", time.Unix(k, 0).UTC().Format("15:04:05"), t.Count,
				float64(t.Sum.Microseconds())/1000/float64(t.Count), float64(t.Max.Microseconds())/1000, t.Slow, t.Errors)
			if r.URL.Query().Get("routes") != "" && len(t.SlowRoutes) > 0 {
				fmt.Fprintf(w, "    %v\n", t.SlowRoutes)
			}
		}
		routeStatsMu.Unlock()
	})
}
