package flights

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDeepLinks(t *testing.T) {
	ret := date("2026-11-17")
	tests := []struct {
		name string
		q    Query
		want []Link
	}{
		{
			name: "round trip with IATA on both ends gets Google and Skyscanner",
			q: Query{
				Origin: Place{Name: "New York", IATA: "JFK"}, Destination: Place{Name: "Lisbon", IATA: "LIS"},
				Depart: date("2026-11-10"), Return: &ret, Passengers: 2, Cabin: CabinEconomy,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights/search?q=Flights+to+LIS+from+JFK+on+2026-11-10+through+2026-11-17+economy"},
				{Provider: "skyscanner", Label: "Skyscanner", URL: "https://www.skyscanner.net/transport/flights/jfk/lis/261110/261117/?adultsv2=2&cabinclass=economy&rtn=1"},
			},
		},
		{
			name: "one way with names only gets Google alone",
			q: Query{
				Origin: Place{Name: "Porto"}, Destination: Place{Name: "Warsaw"},
				Depart: date("2026-12-01"), Passengers: 1,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights/search?q=Flights+to+Warsaw+from+Porto+on+2026-12-01+one+way"},
			},
		},
		{
			name: "a malformed IATA is not used anywhere",
			q: Query{
				Origin: Place{Name: "Lisbon", IATA: "lis"}, Destination: Place{Name: "Rome", IATA: "FCO"},
				Depart: date("2026-12-01"), Passengers: 0, Cabin: CabinBusiness,
			},
			want: []Link{
				{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights/search?q=Flights+to+FCO+from+Lisbon+on+2026-12-01+one+way+business+class"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, DeepLinks{}.Links(tt.q))
		})
	}
}

func TestSkyscannerOneWayAndCabins(t *testing.T) {
	links := DeepLinks{}.Links(Query{
		Origin: Place{Name: "Lisbon", IATA: "LIS"}, Destination: Place{Name: "Rome", IATA: "FCO"},
		Depart: date("2026-12-01"), Passengers: 1, Cabin: CabinPremiumEconomy,
	})
	require.Len(t, links, 2)
	require.Equal(t, "https://www.skyscanner.net/transport/flights/lis/fco/261201/?adultsv2=1&cabinclass=premiumeconomy&rtn=0", links[1].URL)
}

// Checked by hand in Chrome on 2026-10-01: Google drops the whole query and
// shows its home page when q carries a passenger count or "premium economy",
// so neither may reach the link. Skyscanner carries both.
func TestGoogleOmitsWhatBreaksItsParser(t *testing.T) {
	links := DeepLinks{}.Links(Query{
		Origin: Place{Name: "Lisbon", IATA: "LIS"}, Destination: Place{Name: "Rome", IATA: "FCO"},
		Depart: date("2026-12-01"), Passengers: 3, Cabin: CabinPremiumEconomy,
	})
	require.Equal(t, "https://www.google.com/travel/flights/search?q=Flights+to+FCO+from+LIS+on+2026-12-01+one+way", links[0].URL)
	require.Contains(t, links[1].URL, "adultsv2=3&cabinclass=premiumeconomy")
}

func TestValidIATA(t *testing.T) {
	require.True(t, ValidIATA("LIS"))
	for _, s := range []string{"", "lis", "LISB", "L1S", " LIS"} {
		require.False(t, ValidIATA(s), s)
	}
}
