package flights

// metro is a city code that covers several airports. Most flight sites search
// all of them at once (LON is Heathrow, Gatwick, Stansted, Luton, City and
// Southend).
type metro struct {
	IATA    string
	City    string
	Country string
	// Lat and Lon are the city centre: a metro is only offered for a page
	// near it, so London, Ontario does not get LON.
	Lat, Lon float64
	// Skyscanner names metro areas with its own codes, not IATA's.
	Skyscanner string
	// Aliases are other names travellers use for the city.
	Aliases []string
}

// metros is hand-kept: OurAirports lists airports, not city codes. Only
// cities whose airports are far enough apart that picking one would be a
// real choice, and only real city codes (Shanghai's SHA is Hongqiao airport,
// so Shanghai is not here).
var metros = []metro{
	{"LON", "London", "GB", 51.507, -0.128, "lond", nil},
	{"NYC", "New York", "US", 40.713, -74.006, "nyca", []string{"new york city", "nyc"}},
	{"PAR", "Paris", "FR", 48.857, 2.352, "pari", nil},
	{"MIL", "Milan", "IT", 45.464, 9.190, "mila", []string{"milano"}},
	{"ROM", "Rome", "IT", 41.903, 12.496, "rome", []string{"roma"}},
	{"TYO", "Tokyo", "JP", 35.690, 139.692, "tyoa", nil},
	{"OSA", "Osaka", "JP", 34.694, 135.502, "osaa", nil},
	{"SEL", "Seoul", "KR", 37.567, 126.978, "sela", nil},
	{"BJS", "Beijing", "CN", 39.904, 116.407, "bjsa", nil},
	{"CHI", "Chicago", "US", 41.878, -87.630, "chia", nil},
	{"WAS", "Washington", "US", 38.907, -77.037, "wasa", []string{"washington dc", "washington d.c."}},
	{"MOW", "Moscow", "RU", 55.756, 37.617, "mosc", []string{"moskva"}},
	{"STO", "Stockholm", "SE", 59.329, 18.069, "stoc", nil},
	{"BUE", "Buenos Aires", "AR", -34.604, -58.382, "buea", nil},
	{"SAO", "São Paulo", "BR", -23.551, -46.633, "saoa", nil},
	{"RIO", "Rio de Janeiro", "BR", -22.907, -43.173, "rioa", nil},
	{"YTO", "Toronto", "CA", 43.653, -79.383, "ytoa", nil},
	{"YMQ", "Montreal", "CA", 45.502, -73.567, "ymqa", []string{"montréal"}},
}

// skyscannerCode is the code Skyscanner's URL takes for a place: its own
// city code for a metro, the IATA code (lower-cased) otherwise.
func skyscannerCode(iata string) string {
	for _, m := range metros {
		if m.IATA == iata {
			return m.Skyscanner
		}
	}
	return lowerASCII(iata)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
