// Package flights builds links to flight search sites. It never quotes a
// price: v1 hands the traveller a prefilled search on a site that sells the
// ticket. The research behind this (2026-09-30) found no free live-fare API an
// app with no users can get: Amadeus Self-Service shut down on 2026-07-17 and
// Kiwi wants 50k monthly users. Provider is the seam a paid source would use.
package flights

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cabin matches loci.trip.FlightCabin value for value.
type Cabin int

const (
	CabinUnspecified Cabin = iota
	CabinEconomy
	CabinPremiumEconomy
	CabinBusiness
	CabinFirst
)

// Place is one end of a flight. IATA is optional.
type Place struct {
	Name string
	IATA string
}

// Query is one flight search.
type Query struct {
	Origin      Place
	Destination Place
	Depart      time.Time
	Return      *time.Time
	Passengers  int
	Cabin       Cabin
}

// Link is a prefilled search on one site.
type Link struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	URL      string `json:"url"`
}

// Provider turns a query into links.
type Provider interface {
	Links(Query) []Link
}

var iataRE = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidIATA reports whether s looks like an IATA airport or city code. It
// checks shape only; an LLM-supplied code that is well formed but wrong still
// yields a search the traveller can correct on the site.
func ValidIATA(s string) bool { return iataRE.MatchString(s) }

// DeepLinks builds Google Flights links always, and Skyscanner links when both
// ends carry a valid IATA code (Skyscanner's URL takes codes, not names).
type DeepLinks struct{}

func (DeepLinks) Links(q Query) []Link {
	links := []Link{googleFlights(q)}
	if l, ok := skyscanner(q); ok {
		links = append(links, l)
	}
	return links
}

// googleFlights uses the natural-language q= form. The structured tfs=
// parameter is an undocumented encoded blob; q= takes names or codes.
//
// What q= understands was checked by hand on 2026-10-01: route, dates, "one
// way" and the cabins economy / business class / first class. A passenger
// count or "premium economy" makes Google drop the whole query and show its
// home page, so those are left for the traveller to set on the site.
func googleFlights(q Query) Link {
	var b strings.Builder
	fmt.Fprintf(&b, "Flights to %s from %s on %s", placeTerm(q.Destination), placeTerm(q.Origin), q.Depart.Format(time.DateOnly))
	if q.Return != nil {
		fmt.Fprintf(&b, " through %s", q.Return.Format(time.DateOnly))
	} else {
		b.WriteString(" one way")
	}
	if c := googleCabin(q.Cabin); c != "" {
		b.WriteString(" " + c)
	}
	v := url.Values{"q": {b.String()}}
	return Link{Provider: "google_flights", Label: "Google Flights", URL: "https://www.google.com/travel/flights/search?" + v.Encode()}
}

// placeTerm prefers the code: "Porto" alone can land on Porto Alegre.
func placeTerm(p Place) string {
	if ValidIATA(p.IATA) {
		return p.IATA
	}
	return p.Name
}

func googleCabin(c Cabin) string {
	switch c {
	case CabinEconomy:
		return "economy"
	case CabinBusiness:
		return "business class"
	case CabinFirst:
		return "first class"
	default:
		return ""
	}
}

func skyscanner(q Query) (Link, bool) {
	if !ValidIATA(q.Origin.IATA) || !ValidIATA(q.Destination.IATA) {
		return Link{}, false
	}
	u := fmt.Sprintf("https://www.skyscanner.net/transport/flights/%s/%s/%s/",
		strings.ToLower(q.Origin.IATA), strings.ToLower(q.Destination.IATA), q.Depart.Format("060102"))
	rtn := "0"
	if q.Return != nil {
		u += q.Return.Format("060102") + "/"
		rtn = "1"
	}
	v := url.Values{
		"adultsv2":   {strconv.Itoa(max(q.Passengers, 1))},
		"cabinclass": {skyscannerCabin(q.Cabin)},
		"rtn":        {rtn},
	}
	return Link{Provider: "skyscanner", Label: "Skyscanner", URL: u + "?" + v.Encode()}, true
}

func skyscannerCabin(c Cabin) string {
	switch c {
	case CabinPremiumEconomy:
		return "premiumeconomy"
	case CabinBusiness:
		return "business"
	case CabinFirst:
		return "first"
	default:
		return "economy"
	}
}
