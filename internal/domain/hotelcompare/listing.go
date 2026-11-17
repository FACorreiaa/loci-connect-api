// Package hotelcompare answers "which of these should I book?" for hotel
// links a traveller pastes into a chat.
//
// It never reads a booking site's page: those sites answer a server with a
// bot challenge, and what a page says would be untrusted text anyway. A link
// is followed only as far as the redirect that names the property. The report
// is then written from what Loci can check itself: the booking partner's
// record of the hotel, the map around it, the traveller's own words about the
// price, and, when switched on, a web search whose pages are cited.
package hotelcompare

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/FACorreiaa/loci-connect-api/pkg/safefetch"
)

const (
	// MaxLinks is how many listings one message is compared across.
	MaxLinks = 3
	// maxNoteRunes matches TripStay.price_note, where a note may end up.
	maxNoteRunes   = 120
	resolveTimeout = 4 * time.Second
)

// Link is one hotel listing link in a message.
type Link struct {
	// URL is the link as pasted, with https:// added when it had no scheme.
	URL string
	// Note is what the traveller wrote right after the link, as written:
	// usually the price ("385 euros with breakfast").
	Note string
}

// Listing is a Link followed to the property it names.
type Listing struct {
	Link
	// Source is the site, e.g. "booking.com".
	Source string
	// Resolved is false when the link led nowhere that names a property.
	Resolved bool
	// Name is the property's name as the site's address spells it
	// ("Verdi 254"). It has lost its accents and may have lost words.
	Name string
	// CountryCode is ISO 3166-1 alpha-2, upper case; empty when unknown.
	CountryCode string
	// PageURL is the property's own page on the site, https, without
	// tracking parameters. It is what a saved stay links to.
	PageURL string
}

// site reads one booking site's links.
type site interface {
	name() string
	// owns reports whether u is one of the site's links at all.
	owns(u *url.URL) bool
	// property reads a URL that is a property's own page.
	property(u *url.URL) (name, countryCode, pageURL string, ok bool)
}

// sites is every booking site whose links are read. Adding one is adding it
// here: detection, resolution and the fetch allowlist all follow from it.
var sites = []site{booking{}}

func siteFor(u *url.URL) site {
	for _, s := range sites {
		if s.owns(u) {
			return s
		}
	}
	return nil
}

type booking struct{}

func (booking) name() string { return "booking.com" }

func (booking) owns(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "booking.com" || strings.HasSuffix(host, ".booking.com")
}

var (
	// /hotel/es/verdi-254.en-gb.html
	bookingProperty = regexp.MustCompile(`^/hotel/([a-z]{2})/([a-z0-9][a-z0-9-]{0,150})((?:\.[a-z]{2}(?:-[a-z]{2,4})?)?)\.html$`)
	slugWord        = regexp.MustCompile(`[a-z0-9]+`)
)

func (booking) property(u *url.URL) (name, countryCode, pageURL string, ok bool) {
	m := bookingProperty.FindStringSubmatch(strings.ToLower(u.Path))
	if m == nil {
		return "", "", "", false
	}
	words := slugWord.FindAllString(m[2], -1)
	if len(words) == 0 {
		return "", "", "", false
	}
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " "), strings.ToUpper(m[1]), "https://www.booking.com/hotel/" + m[1] + "/" + m[2] + ".html", true
}

var (
	linkRe = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"'` + "`" + `]+`)
	// What ends a link when it is followed by punctuation the traveller
	// typed, not part of the address.
	linkTail = ".,;:!?)]}>"
	// Words that join two links, not part of what is said about either.
	joiners = regexp.MustCompile(`(?i)^(?:vs\.?|versus|or|and|ou|e|y|o|et|und|oder|against|compared (?:to|with)|[-–—,;:/&|])$`)
)

// FindLinks returns the hotel listing links in a message, in order, without
// repeats, at most MaxLinks. A link to anything else is not one.
func FindLinks(text string) []Link {
	var out []Link
	seen := map[string]bool{}
	locs := linkRe.FindAllStringIndex(text, -1)
	for i, loc := range locs {
		raw := strings.TrimRight(text[loc[0]:loc[1]], linkTail)
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || siteFor(u) == nil {
			continue
		}
		u.Scheme = "https"
		key := strings.ToLower(u.Host) + u.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, Link{URL: u.String(), Note: noteAfter(text[loc[0]+len(strings.TrimRight(text[loc[0]:loc[1]], linkTail)) : end])})
		if len(out) == MaxLinks {
			break
		}
	}
	return out
}

// noteAfter is what the traveller said about a link: the words after it up
// to the end of the sentence, without the word that joins it to the next
// link.
func noteAfter(s string) string {
	for i, r := range s {
		if r == '\n' || r == '?' || r == '!' {
			s = s[:i]
			break
		}
		// A full stop ends the note unless it is inside a number (3.5).
		if r == '.' && (i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\n') {
			s = s[:i]
			break
		}
	}
	words := strings.Fields(s)
	for len(words) > 0 && joiners.MatchString(words[len(words)-1]) {
		words = words[:len(words)-1]
	}
	for len(words) > 0 && joiners.MatchString(words[0]) {
		words = words[1:]
	}
	return clip(strings.Join(words, " "), maxNoteRunes)
}

// Redirects follows a link until a hop the caller accepts (*safefetch.Fetcher).
type Redirects interface {
	GetUntil(ctx context.Context, rawURL string, stop func(*url.URL) bool) (safefetch.Page, error)
}

// Resolve follows each link to the property it names. Only redirects are
// followed, and only between addresses of the site the link is on; the
// property's page itself is never requested. A link that cannot be followed
// comes back unresolved, never as an error: the comparison goes on with what
// the traveller wrote.
func Resolve(ctx context.Context, fetch Redirects, links []Link) []Listing {
	out := make([]Listing, len(links))
	var wg sync.WaitGroup
	for i, l := range links {
		out[i] = Listing{Link: l}
		u, err := url.Parse(l.URL)
		if err != nil {
			continue
		}
		s := siteFor(u)
		if s == nil {
			continue
		}
		out[i].Source = s.name()
		if name, cc, page, ok := s.property(u); ok {
			out[i].Resolved, out[i].Name, out[i].CountryCode, out[i].PageURL = true, name, cc, page
			continue
		}
		if fetch == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
			defer cancel()
			// Stop at the property's page, and at anything off the site: a
			// redirect that leaves it is not followed.
			page, _ := fetch.GetUntil(ctx, l.URL, func(hop *url.URL) bool {
				if !s.owns(hop) {
					return true
				}
				_, _, _, ok := s.property(hop)
				return ok
			})
			final, err := url.Parse(page.FinalURL)
			if err != nil || !s.owns(final) {
				return
			}
			if name, cc, pageURL, ok := s.property(final); ok {
				out[i].Resolved, out[i].Name, out[i].CountryCode, out[i].PageURL = true, name, cc, pageURL
			}
		}()
	}
	wg.Wait()
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}
