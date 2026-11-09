package flights

// metro is a city code that covers several airports. Skyscanner and most
// flight sites search all of them at once (LON is Heathrow, Gatwick, Stansted,
// Luton, City and Southend).
type metro struct {
	IATA    string
	City    string
	Country string
	// Aliases are other names travellers use for the city.
	Aliases []string
}

// metros is hand-kept: OurAirports lists airports, not city codes. Only cities
// whose airports are far enough apart that picking one would be a real choice.
var metros = []metro{
	{"LON", "London", "GB", nil},
	{"NYC", "New York", "US", []string{"new york city", "nyc"}},
	{"PAR", "Paris", "FR", nil},
	{"MIL", "Milan", "IT", []string{"milano"}},
	{"ROM", "Rome", "IT", []string{"roma"}},
	{"TYO", "Tokyo", "JP", nil},
	{"OSA", "Osaka", "JP", nil},
	{"SEL", "Seoul", "KR", nil},
	{"BJS", "Beijing", "CN", nil},
	{"SHA", "Shanghai", "CN", nil},
	{"CHI", "Chicago", "US", nil},
	{"WAS", "Washington", "US", []string{"washington dc", "washington d.c."}},
	{"MOW", "Moscow", "RU", []string{"moskva"}},
	{"STO", "Stockholm", "SE", nil},
	{"BUE", "Buenos Aires", "AR", nil},
	{"SAO", "São Paulo", "BR", nil},
	{"RIO", "Rio de Janeiro", "BR", nil},
	{"YTO", "Toronto", "CA", nil},
	{"YMQ", "Montreal", "CA", []string{"montréal"}},
}
