package localcontext

import "strings"

// countryNames maps the names people type into a profile's free-text country
// field to ISO 3166-1 alpha-2. Not exhaustive: a name not here yields no news
// for that country, which is the safe failure. Extend when a real profile
// shows up with a spelling that misses.
var countryNames = map[string]string{
	"portugal": "PT", "spain": "ES", "españa": "ES", "france": "FR", "germany": "DE", "deutschland": "DE",
	"italy": "IT", "italia": "IT", "united kingdom": "GB", "uk": "GB", "great britain": "GB", "england": "GB",
	"ireland": "IE", "netherlands": "NL", "the netherlands": "NL", "holland": "NL", "belgium": "BE",
	"switzerland": "CH", "austria": "AT", "poland": "PL", "czech republic": "CZ", "czechia": "CZ",
	"sweden": "SE", "norway": "NO", "denmark": "DK", "finland": "FI", "iceland": "IS", "greece": "GR",
	"turkey": "TR", "türkiye": "TR", "croatia": "HR", "hungary": "HU", "romania": "RO", "bulgaria": "BG",
	"united states": "US", "united states of america": "US", "usa": "US", "us": "US", "america": "US",
	"canada": "CA", "mexico": "MX", "méxico": "MX", "brazil": "BR", "brasil": "BR", "argentina": "AR",
	"chile": "CL", "colombia": "CO", "peru": "PE", "perú": "PE", "uruguay": "UY",
	"japan": "JP", "china": "CN", "south korea": "KR", "korea": "KR", "india": "IN", "thailand": "TH",
	"vietnam": "VN", "indonesia": "ID", "malaysia": "MY", "singapore": "SG", "philippines": "PH",
	"australia": "AU", "new zealand": "NZ", "south africa": "ZA", "morocco": "MA", "egypt": "EG",
	"united arab emirates": "AE", "uae": "AE", "israel": "IL", "cape verde": "CV", "cabo verde": "CV",
	"angola": "AO", "mozambique": "MZ",
}

// countryDisplay is the English name used in news queries, keyed by code.
var countryDisplay = map[string]string{}

func init() {
	// Prefer the shortest common English spelling for each code.
	for name, code := range countryNames {
		cur, ok := countryDisplay[code]
		if !ok || len(name) < len(cur) && !strings.ContainsAny(name, "éñü") {
			countryDisplay[code] = name
		}
	}
	// Overrides where the shortest key is an abbreviation.
	for code, name := range map[string]string{"US": "United States", "GB": "United Kingdom", "AE": "United Arab Emirates", "NL": "Netherlands", "CZ": "Czechia", "TR": "Turkey", "MX": "Mexico", "PE": "Peru", "ES": "Spain", "DE": "Germany", "IT": "Italy", "BR": "Brazil", "KR": "South Korea"} {
		countryDisplay[code] = name
	}
}

// countryCodeFromName accepts an alpha-2 code (any case) or a known English
// or native country name. Unknown input yields "".
func countryCodeFromName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if len(s) == 2 {
		up := strings.ToUpper(s)
		if _, known := countryDisplay[up]; known {
			return up
		}
		// Unknown two-letter values are still treated as codes: the profile
		// may hold a country this table has not learned a name for.
		if s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' {
			return up
		}
		return ""
	}
	return countryNames[s]
}

// countryName is the English name for a code, or the code itself.
func countryName(code string) string {
	if name, ok := countryDisplay[code]; ok {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return code
}
