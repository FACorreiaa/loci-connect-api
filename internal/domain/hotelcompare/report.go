package hotelcompare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/FACorreiaa/loci-connect-api/pkg/hotelrates"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

var (
	// ErrNoLinks means the message has no hotel listing link in it.
	ErrNoLinks = errors.New("hotelcompare: no hotel links in the message")
	// ErrDailyLimit means the traveller has had today's comparisons.
	ErrDailyLimit = errors.New("hotelcompare: daily comparison limit reached")
)

const (
	evidenceTimeout = 10 * time.Second
	intakeTimeout   = 15 * time.Second
	writeTimeout    = 45 * time.Second
	maxFacilities   = 10
	maxReportStops  = 6
	defaultAdults   = 2
	defaultCurrency = "EUR"
)

// ToolCaller is one Anthropic call with tools (*ai.ToolClient). The report is
// only ever written there: the message and the traveller's prices must not
// reach a free model that may log prompts.
type ToolCaller interface {
	ToolTurn(ctx context.Context, req anthropic.ToolTurnRequest, onText func(string)) (*anthropic.ToolTurnResult, error)
}

// Map is what is known about a point and a name on the map (*transit.Overpass).
type Map interface {
	Around(ctx context.Context, p transit.LatLon) (transit.Surroundings, error)
	FindLodging(ctx context.Context, name string, near transit.LatLon) (transit.Place, bool, error)
	FindAddress(ctx context.Context, street, number string, near transit.LatLon) (transit.Place, bool, error)
}

// Config is the feature's switches.
type Config struct {
	// WebSearch lets the report call search the web and cite what it read.
	WebSearch bool
	// WebSearchMaxUses caps searches in one report. Zero means 3.
	WebSearchMaxUses int
	// WebSearchDaily caps reports that may search, across everyone, per UTC
	// day. Zero means no cap. Past it reports are written without the web.
	WebSearchDaily int
	// PerUserDaily caps one traveller's comparisons per UTC day. Zero means
	// no cap.
	PerUserDaily int
}

// Deps is everything a comparison draws on. Every field but Now and Logger
// may be nil: the report is then written from what is left, and says what
// it could not check.
type Deps struct {
	Fetch   Redirects
	Partner hotelrates.Identifier
	Rates   hotelrates.Provider
	Map     Map
	Places  geocode.Forward
	LLM     ToolCaller
	// CanSpend is asked before the model is used at all. False (the day's
	// ceiling is reached) writes the report from the facts alone. Nil means
	// always.
	CanSpend func(ctx context.Context) bool
	// Spent is told what each model call cost, for the daily spend ledger.
	Spent  func(ctx context.Context, userID uuid.UUID, model string, u anthropic.ToolUsage)
	Config Config
	Logger *slog.Logger
	Now    func() time.Time
}

// Request is one comparison.
type Request struct {
	UserID uuid.UUID
	// Text is the traveller's message, links included.
	Text string
	// CityHint is the city the chat is already about (the trip's, or the
	// request's), used when the message names none.
	CityHint string
	// CheckIn and CheckOut are the trip's dates, used when the message
	// states none.
	CheckIn, CheckOut *time.Time
}

// Hotel is one compared listing.
type Hotel struct {
	Listing
	// Identified is true when the listing was matched to a hotel on record,
	// by the booking partner or the map.
	Identified bool
	// Located is true when it at least has a position; a flat listed by its
	// address is located without being identified.
	Located bool
	// Stay is ready to save to a trip.
	Stay trip.TripStay
	// Rating is the partner's guest score out of 10 over ReviewCount reviews;
	// both zero when it has none.
	Rating      float64
	ReviewCount int
	Facilities  []string
	LivePrice   *hotelrates.Rate
	Stops       []transit.Stop
	Highlights  []transit.Highlight
	// Written by the model; empty when no report could be written.
	Neighbourhood string
	GettingAround string
	Pros, Cons    []string

	partnerID string
}

// Row is one line of the side-by-side table: a value per hotel, in order.
type Row struct {
	Label  string
	Values []string
}

// Source is a page the report drew on.
type Source struct {
	Title     string
	URL       string
	Publisher string
}

// Report is the answer.
type Report struct {
	ID     uuid.UUID
	Hotels []Hotel
	// Verdict is empty when none could be written.
	Verdict string
	// Recommended indexes Hotels; nil when the verdict favours neither.
	Recommended   *int
	Rows          []Row
	Caveats       []string
	Sources       []Source
	WebSearchUsed bool
	GeneratedAt   time.Time
	City          string
	CheckIn       *time.Time
	CheckOut      *time.Time
}

// Service compares hotel listings.
type Service struct {
	d      Deps
	users  dayCounter
	search dayCounter
}

func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Config.WebSearchMaxUses <= 0 {
		d.Config.WebSearchMaxUses = 3
	}
	return &Service{d: d}
}

// The stages Compare reports, as codes a chat turns into its own wording.
const (
	StageLinks  = "hotel_links"
	StageLookup = "hotel_lookup"
	StageWrite  = "hotel_report"
)

// Event is a finished comparison on its way to a client: the report, and
// the proposal that offers its hotels as the stay, when one of the
// traveller's trips has the city.
type Event struct {
	Report         *Report `json:"report"`
	StayProposalID string  `json:"stay_proposal_id,omitempty"`
}

// HasLinks reports whether a message is one Compare would answer. It is
// cheap: callers use it to decide whether a turn is a comparison at all.
func HasLinks(text string) bool { return len(FindLinks(text)) > 0 }

// Compare answers a message that has hotel links in it. progress, when set,
// is told each Stage as it starts, for a chat that shows what is happening.
func (s *Service) Compare(ctx context.Context, req Request, progress func(string)) (*Report, error) {
	say := func(stage string) {
		if progress != nil {
			progress(stage)
		}
	}
	links := FindLinks(req.Text)
	if len(links) == 0 {
		return nil, ErrNoLinks
	}
	now := s.d.Now().UTC()
	if !s.users.take(req.UserID.String(), now, s.d.Config.PerUserDaily) {
		return nil, ErrDailyLimit
	}

	say(StageLinks)
	listings := Resolve(ctx, s.d.Fetch, links)

	llm := s.d.LLM
	if llm != nil && s.d.CanSpend != nil && !s.d.CanSpend(ctx) {
		llm = nil
	}
	in := s.intake(ctx, llm, req, listings, now)
	rep := &Report{ID: uuid.New(), GeneratedAt: now, City: in.city, CheckIn: in.checkIn, CheckOut: in.checkOut}

	say(StageLookup)
	centre, hasCentre := s.centre(ctx, &rep.City, listings)
	rep.Hotels = make([]Hotel, len(listings))
	var wg sync.WaitGroup
	for i, l := range listings {
		l.Note = in.notes[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ectx, cancel := context.WithTimeout(ctx, evidenceTimeout)
			defer cancel()
			rep.Hotels[i] = s.gather(ectx, i, l, rep, centre, hasCentre)
		}()
	}
	wg.Wait()

	say(StageWrite)
	wrote := s.write(ctx, llm, req, rep, now)
	rep.Rows = rows(rep)
	rep.Caveats = append(serverCaveats(rep, wrote), rep.Caveats...)
	if len(rep.Caveats) > maxCaveats {
		rep.Caveats = rep.Caveats[:maxCaveats]
	}
	return rep, nil
}

// centre is where the stay's city is. It prefers the place in the country a
// link named, and corrects the city's spelling to the geocoder's.
func (s *Service) centre(ctx context.Context, city *string, listings []Listing) (transit.LatLon, bool) {
	if s.d.Places == nil || strings.TrimSpace(*city) == "" {
		return transit.LatLon{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	places, err := s.d.Places.Search(ctx, *city, 5)
	if err != nil || len(places) == 0 {
		return transit.LatLon{}, false
	}
	countries := map[string]bool{}
	for _, l := range listings {
		if l.CountryCode != "" {
			countries[l.CountryCode] = true
		}
	}
	pick := -1
	for i, p := range places {
		if countries[strings.ToUpper(p.CountryCode)] {
			pick = i
			break
		}
	}
	if pick < 0 {
		if len(countries) > 0 {
			// The links are in a country this city is not in: the message is
			// about somewhere else than the hint says.
			return transit.LatLon{}, false
		}
		pick = 0
	}
	*city = places[pick].Name
	return transit.LatLon{Lat: places[pick].Lat, Lon: places[pick].Lon}, true
}

var addressLike = regexp.MustCompile(`^(.*\D)\s+(\d{1,5}[A-Za-z]?)$`)

// gather finds out what it can about one listing.
func (s *Service) gather(ctx context.Context, i int, l Listing, rep *Report, centre transit.LatLon, hasCentre bool) Hotel {
	h := Hotel{Listing: l}
	h.Stay = trip.TripStay{CityName: rep.City, Name: l.Name, CheckIn: rep.CheckIn, CheckOut: rep.CheckOut}
	if h.Stay.Name == "" {
		h.Stay.Name = fmt.Sprintf("Hotel from link %d", i+1)
	}
	if l.PageURL != "" {
		page := l.PageURL
		h.Stay.BookingURL = &page
	}
	if l.Note != "" {
		note := l.Note
		h.Stay.PriceNote = &note
	}
	if !l.Resolved || rep.City == "" {
		return h
	}

	pin := func(lat, lon float64) {
		h.Located = true
		h.Stay.Lat, h.Stay.Lon = &lat, &lon
	}
	if s.d.Partner != nil {
		found, ok, err := s.d.Partner.Identify(ctx, hotelrates.Query{Name: l.Name, City: rep.City, CountryCode: l.CountryCode})
		if err != nil {
			s.d.Logger.WarnContext(ctx, "hotel compare: partner lookup failed", slog.Any("error", err))
		}
		if ok {
			h.Identified, h.partnerID = true, found.ID
			h.Stay.Name, h.Stay.Address = clip(found.Name, 300), clip(found.Address, 300)
			if found.Stars > 0 {
				h.Stay.StarRating = strconv.FormatFloat(found.Stars, 'f', -1, 64)
			}
			h.Rating, h.ReviewCount = found.Rating, found.ReviewCount
			pin(found.Lat, found.Lon)
		}
	}
	if !h.Identified && s.d.Map != nil && hasCentre {
		if p, ok, err := s.d.Map.FindLodging(ctx, l.Name, centre); err == nil && ok {
			h.Identified = true
			h.Stay.Name, h.Stay.Address, h.Stay.StarRating = p.Name, p.Address, p.Stars
			pin(p.Lat, p.Lon)
		} else if m := addressLike.FindStringSubmatch(l.Name); m != nil && err == nil {
			if p, ok, err := s.d.Map.FindAddress(ctx, m[1], m[2], centre); err == nil && ok {
				h.Stay.Address = p.Address
				pin(p.Lat, p.Lon)
			}
		}
	}
	if !h.Located {
		return h
	}

	at := transit.LatLon{Lat: *h.Stay.Lat, Lon: *h.Stay.Lon}
	var wg sync.WaitGroup
	if s.d.Map != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			around, err := s.d.Map.Around(ctx, at)
			if err != nil {
				return
			}
			h.Stops, h.Highlights = around.Stops, around.Highlights
			if len(h.Stops) > maxReportStops {
				h.Stops = h.Stops[:maxReportStops]
			}
		}()
	}
	if h.partnerID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f, err := s.d.Partner.Facilities(ctx, h.partnerID, maxFacilities); err == nil {
				h.Facilities = f
			}
		}()
		if s.d.Rates != nil && rep.CheckIn != nil && rep.CheckOut != nil && rep.CheckOut.After(*rep.CheckIn) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rates, err := s.d.Rates.Rates(ctx,
					[]hotelrates.Query{{Key: "h", Name: h.Stay.Name, Lat: at.Lat, Lon: at.Lon, City: rep.City, CountryCode: l.CountryCode}},
					hotelrates.Stay{Checkin: *rep.CheckIn, Checkout: *rep.CheckOut, Adults: defaultAdults, Currency: defaultCurrency})
				if err == nil && len(rates) == 1 && rates[0].Status == hotelrates.StatusPriced {
					h.LivePrice = &rates[0]
				}
			}()
		}
	}
	wg.Wait()
	return h
}

// dayCounter counts per key per UTC day, in this process. Loci runs one API
// replica; a restart forgives the day, which errs towards the traveller.
type dayCounter struct {
	mu   sync.Mutex
	day  string
	used map[string]int
}

func (c *dayCounter) take(key string, now time.Time, limit int) bool {
	if limit <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if today := now.Format(time.DateOnly); today != c.day {
		c.day, c.used = today, map[string]int{}
	}
	if c.used[key] >= limit {
		return false
	}
	c.used[key]++
	return true
}
