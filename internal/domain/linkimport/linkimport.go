// Package linkimport turns a link someone shared (a TikTok, a reel, a Maps pin,
// a blog post) into the places it mentions, matched to Loci places.
//
// The page is fetched through safefetch, read without running anything,
// handed to the model as fenced data for extraction, and every place named is
// looked up the way search does. Nothing is saved here: the client picks
// which places go into which list.
package linkimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/FACorreiaa/loci-connect-api/pkg/safefetch"
)

var (
	// ErrBadURL is a link that is not a public http(s) page.
	ErrBadURL = errors.New("not a link Loci can open")
	// ErrUnreadable is a page that could not be fetched or held no text.
	ErrUnreadable = errors.New("couldn't read that link")
	// ErrNoPlaces is a readable page that names no places.
	ErrNoPlaces = errors.New("no places found on that page")
	// ErrModel is extraction failing: the model errored or never gave JSON.
	ErrModel = errors.New("couldn't read the places right now")
)

const (
	resolveBudget      = 25 * time.Second
	resolveConcurrency = 3
	maxTitleRunes      = 120
)

// Source is where a link came from; it decides how the link is read.
type Source int

const (
	SourceWeb Source = iota
	SourceGoogleMaps
	SourceTikTok
	SourceInstagram
	SourceYouTube
)

// Fetcher is safefetch.Fetcher.
type Fetcher interface {
	Get(ctx context.Context, rawURL string) (safefetch.Page, error)
}

// Generator is one text completion (chat's ActionLLM).
type Generator interface {
	GenerateText(ctx context.Context, prompt string) (string, error)
}

// Resolver finds, or creates, the Loci place a candidate names. A nil place
// and nil error means there is no such place.
type Resolver interface {
	Resolve(ctx context.Context, c Candidate) (*Place, error)
}

// Candidate is a place the link names. Lat and Lon are set only when the
// link itself pins it (a Maps link).
type Candidate struct {
	Name     string
	City     string
	Note     string
	Lat, Lon *float64
}

// Place is the Loci place a candidate resolved to.
type Place struct {
	ID        string
	Name      string
	Address   string
	Category  string
	Latitude  float64
	Longitude float64
}

// Imported is one place in the answer; Place is nil when it was not found.
type Imported struct {
	Name  string
	City  string
	Note  string
	Place *Place
}

// Result is what a link held.
type Result struct {
	Source Source
	Title  string
	City   string
	Places []Imported
}

// Service imports places from links.
type Service struct {
	fetch   Fetcher
	gen     Generator
	resolve Resolver
	log     *slog.Logger
}

// NewService builds the service.
func NewService(fetch Fetcher, gen Generator, resolve Resolver, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{fetch: fetch, gen: gen, resolve: resolve, log: log.With(slog.String("component", "link-import"))}
}

// Import reads rawURL and returns the places it mentions, in its order.
func (s *Service) Import(ctx context.Context, rawURL, cityHint string) (*Result, error) {
	u, err := parseLink(rawURL)
	if err != nil {
		return nil, err
	}
	cityHint = clip(cityHint, maxCityRunes)
	source := detectSource(u)

	var (
		doc    Doc
		pinned *Candidate
	)
	switch source {
	case SourceGoogleMaps:
		pinned, err = s.readMaps(ctx, u)
		if pinned != nil {
			doc = mapsDoc(*pinned)
		}
	case SourceTikTok:
		doc, err = s.readTikTok(ctx, u)
	default:
		doc, err = s.readPage(ctx, u.String())
	}
	if err != nil {
		return nil, err
	}

	res := &Result{Source: source, Title: clip(doc.Title, maxTitleRunes)}
	var cands []Candidate
	switch {
	case pinned != nil && cityHint != "":
		// The link names the place and pins it; the hint gives the city.
		pinned.City = cityHint
		cands = []Candidate{*pinned}
	default:
		city, found, err := Extract(ctx, s.gen, doc, cityHint)
		if err != nil {
			s.log.WarnContext(ctx, "link import: extraction failed", slog.Any("error", err))
			return nil, ErrModel
		}
		res.City = city
		cands = found
		if pinned != nil {
			// Only the city is wanted from the model; the name and pin stay
			// as the link gave them.
			c := *pinned
			c.City = firstNonEmpty(cityOf(found), city, cityHint)
			cands = []Candidate{c}
		}
	}
	if len(cands) == 0 {
		return nil, ErrNoPlaces
	}
	if res.City == "" {
		res.City = cands[0].City
	}
	res.Places = s.resolveAll(ctx, cands)
	return res, nil
}

// resolveAll looks every candidate up, a few at a time, inside one budget. A
// lookup that fails or runs out of time leaves the place unresolved, never
// dropped.
func (s *Service) resolveAll(ctx context.Context, cands []Candidate) []Imported {
	ctx, cancel := context.WithTimeout(ctx, resolveBudget)
	defer cancel()
	out := make([]Imported, len(cands))
	sem := make(chan struct{}, resolveConcurrency)
	var wg sync.WaitGroup
	for i, c := range cands {
		out[i] = Imported{Name: c.Name, City: c.City, Note: c.Note}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			p, err := s.resolve.Resolve(ctx, c)
			if err != nil {
				s.log.WarnContext(ctx, "link import: resolve failed", slog.String("name", c.Name), slog.Any("error", err))
				return
			}
			out[i].Place = p
		}()
	}
	wg.Wait()
	return out
}

func (s *Service) readPage(ctx context.Context, link string) (Doc, error) {
	page, err := s.fetch.Get(ctx, link)
	if err != nil {
		return Doc{}, fetchErr(err)
	}
	var doc Doc
	if page.ContentType == "application/json" || page.ContentType == "text/plain" {
		doc = Doc{Text: collapse(string(page.Body))}
	} else {
		doc = readPage(page.Body)
	}
	if doc.empty() {
		return Doc{}, ErrUnreadable
	}
	return doc, nil
}

func fetchErr(err error) error {
	if errors.Is(err, safefetch.ErrBadURL) || errors.Is(err, safefetch.ErrBlocked) {
		return ErrBadURL
	}
	return fmt.Errorf("%w: %w", ErrUnreadable, err)
}

// parseLink accepts what a person pastes: surrounding spaces, and a missing
// scheme on an otherwise plain host.
func parseLink(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") && strings.Contains(raw, ".") && !strings.ContainsAny(raw, " \t") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, ErrBadURL
	}
	return u, nil
}

func cityOf(cs []Candidate) string {
	for _, c := range cs {
		if c.City != "" {
			return c.City
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// clip trims s and cuts it to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
