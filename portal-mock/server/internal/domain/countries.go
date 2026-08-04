package domain

type SupportedCountry struct {
	Code   string
	NameJA string
	Region Region
}

var supportedCountries = []SupportedCountry{
	{Code: "US", NameJA: "アメリカ合衆国", Region: RegionUS},
	{Code: "CA", NameJA: "カナダ", Region: RegionUS},
	{Code: "MX", NameJA: "メキシコ", Region: RegionUS},
	{Code: "JP", NameJA: "日本", Region: RegionAP},
	{Code: "KR", NameJA: "大韓民国", Region: RegionAP},
	{Code: "CN", NameJA: "中華人民共和国", Region: RegionAP},
	{Code: "AU", NameJA: "オーストラリア", Region: RegionAP},
	{Code: "GB", NameJA: "イギリス", Region: RegionEU},
	{Code: "DE", NameJA: "ドイツ", Region: RegionEU},
	{Code: "FR", NameJA: "フランス", Region: RegionEU},
}

var countryByCode = func() map[string]SupportedCountry {
	result := make(map[string]SupportedCountry, len(supportedCountries))
	for _, country := range supportedCountries {
		result[country.Code] = country
	}
	return result
}()

func SupportedCountries() []SupportedCountry {
	return append([]SupportedCountry(nil), supportedCountries...)
}
