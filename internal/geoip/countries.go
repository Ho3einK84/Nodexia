package geoip

import "strings"

// countryNames maps every ISO 3166-1 alpha-2 code to its English name. It is the
// recognised-country allow-list used by ParseResponse / FlagEmoji / CountryName.
//
// To keep the source compact (and avoid one enormous map literal), the data is
// stored as "CC=Name" pairs joined by "|" across a handful of region segments
// and parsed once at init. Editing is a matter of adding a pair to the relevant
// segment.
var countryNames = parseCountryData(
	countryDataAF + "|" +
		countryDataAM + "|" +
		countryDataAS + "|" +
		countryDataEU + "|" +
		countryDataOC,
)

func parseCountryData(data string) map[string]string {
	pairs := strings.Split(data, "|")
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		code, name, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		code = strings.ToUpper(strings.TrimSpace(code))
		name = strings.TrimSpace(name)
		if code == "" || name == "" {
			continue
		}
		out[code] = name
	}
	return out
}
