// Package geo はアプリのリージョンと、ルーターが受け付けるクライアントの国を
// 対応づける（docs/router.md）。ルーターは JP/KR/CN/AU を asia、GB/DE/FR を
// europe、US/CA/MX を america へ振り分ける。
package geo

const (
	Asia    = "asia"
	Europe  = "europe"
	America = "america"
)

// Regions は全リージョンを固定の順で並べたもの。
var Regions = []string{Asia, Europe, America}

var countries = map[string][]string{
	Asia:    {"JP", "KR", "CN", "AU"},
	Europe:  {"GB", "DE", "FR"},
	America: {"US", "CA", "MX"},
}

var regionOf = func() map[string]string {
	out := make(map[string]string)
	for region, list := range countries {
		for _, country := range list {
			out[country] = region
		}
	}
	return out
}()

// Valid は region が既知のリージョンかを返す。
func Valid(region string) bool {
	_, ok := countries[region]
	return ok
}

// Country は region へ振り分けられる国を返す。index でユーザーをリージョン内の
// 国へ決定的に散らす。
func Country(region string, index int) string {
	list := countries[region]
	if len(list) == 0 {
		return ""
	}
	return list[index%len(list)]
}

// RegionOf はルーターが country を振り分けるリージョンを返す。未知の国なら ""。
func RegionOf(country string) string {
	return regionOf[country]
}
