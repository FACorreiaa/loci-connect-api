// Package flights builds links to flight search sites. It never quotes a
// price: v1 hands the traveller a prefilled search on a site that sells the
// ticket. The research behind this (2026-09-30) found no free live-fare API an
// app with no users can get: Amadeus Self-Service shut down on 2026-07-17 and
// Kiwi wants 50k monthly users. Provider is the seam a paid source would use.
package flights

import (
	"fmt"
	"log/slog"
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
	// Sponsored is true when URL carries an affiliate tag.
	Sponsored bool `json:"sponsored,omitempty"`
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

// IATAResolver turns a bare place name into an airport or metro code, or
// reports that it cannot do so without guessing.
type IATAResolver interface {
	ResolveName(name string) (string, bool)
}

// DeepLinks builds Google Flights links always, and Skyscanner links when both
// ends carry a valid IATA code (Skyscanner's URL takes codes, not names).
//
// Resolver, when set, fills a missing code for Skyscanner only: Google keeps
// exactly the place it was given, so a wrong resolution can never break the
// one link that works on names. SkyscannerTemplate, when set, wraps the
// Skyscanner URL for an affiliate network ({url} is replaced by the encoded
// URL). The zero value builds exactly what v1 did.
type DeepLinks struct {
	Resolver           IATAResolver
	SkyscannerTemplate string
}

// NewDeepLinks builds DeepLinks, dropping a template that would produce a
// broken link (no {url}, or not https) with a warning.
func NewDeepLinks(r IATAResolver, skyscannerTemplate string, logger *slog.Logger) DeepLinks {
	tpl := strings.TrimSpace(skyscannerTemplate)
	if tpl != "" && !ValidTemplate(tpl) {
		if logger != nil {
			logger.Warn("ignoring FLIGHTS_LINK_TEMPLATE_SKYSCANNER; it needs https:// and {url}")
		}
		tpl = ""
	}
	return DeepLinks{Resolver: r, SkyscannerTemplate: tpl}
}

// ValidTemplate reports whether t can wrap a link: https, with a {url} slot.
func ValidTemplate(t string) bool {
	return strings.HasPrefix(t, "https://") && strings.Contains(t, "{url}")
}

func (d DeepLinks) Links(q Query) []Link {
	links := []Link{googleFlights(q)}
	if l, ok := skyscanner(d.fill(q)); ok {
		if d.SkyscannerTemplate != "" {
			l.URL = strings.ReplaceAll(d.SkyscannerTemplate, "{url}", url.QueryEscape(l.URL))
			l.Sponsored = true
		}
		links = append(links, l)
	}
	return links
}

// fill adds codes the resolver is sure of to ends that have only a name.
func (d DeepLinks) fill(q Query) Query {
	if d.Resolver == nil {
		return q
	}
	for _, p := range []*Place{&q.Origin, &q.Destination} {
		if ValidIATA(p.IATA) || strings.TrimSpace(p.Name) == "" {
			continue
		}
		if code, ok := d.Resolver.ResolveName(p.Name); ok && ValidIATA(code) {
			p.IATA = code
		}
	}
	return q
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
		skyscannerCode(q.Origin.IATA), skyscannerCode(q.Destination.IATA), q.Depart.Format("060102"))
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
