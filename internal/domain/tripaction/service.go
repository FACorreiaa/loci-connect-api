package tripaction

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
)

const (
	// proposalTTL: after a day the trip has likely moved on, and a card from
	// yesterday's chat should not quietly rewrite today's plan.
	proposalTTL       = 24 * time.Hour
	hotelRadiusMeters = 5000
	maxHotelOptions   = 5
	sourceLabel       = "Trip planner"
	giveBackTimeout   = 5 * time.Second
)

// Trips reads the trip a proposal is about (trip.Repository).
type Trips interface {
	GetTrip(ctx context.Context, id, userID uuid.UUID) (*trip.Trip, error)
}

// Plans is the write path a confirmed proposal goes through (*trip.Service).
type Plans interface {
	SetDates(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, start, end time.Time) (*trip.Trip, error)
	SetStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stay trip.TripStay) (*trip.Trip, error)
	AddFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, f trip.TripFlight) (*trip.Trip, error)
	ReplaceDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, days []trip.TripDay) (*trip.Trip, error)
	FlightLinks(q flights.Query) ([]flights.Link, error)
}

// Hotels is the nearby-hotel search (poi.Service).
type Hotels interface {
	GetNearbyHotels(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, starRating, amenities string) ([]locitypes.POIDetailedInfo, error)
}

// Regenerator plans n days for a city without saving a trip (the chat
// service). The generation's session hangs under parentSessionID, so a
// re-plan never shows up as a search of its own.
type Regenerator interface {
	GenerateDays(ctx context.Context, userID, parentSessionID uuid.UUID, cityName string, days int) ([]trip.TripDay, error)
}

// Sessions is the chat thread a confirmation is posted into.
type Sessions interface {
	GetSession(ctx context.Context, sessionID uuid.UUID) (*locitypes.ChatSession, error)
	AddMessageToSession(ctx context.Context, sessionID uuid.UUID, message locitypes.ConversationMessage) error
}

type Deps struct {
	LLM      Generator
	Store    Store
	Trips    Trips
	Plans    Plans
	Hotels   Hotels
	Places   geocode.Forward
	Regen    Regenerator
	Sessions Sessions
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service proposes, applies and dismisses the agent's trip actions.
type Service struct{ d Deps }

func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{d: d}
}

// Propose turns message into stored proposals for the caller's trip. No
// proposals and a nil error means the message asked for no change.
func (s *Service) Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]Proposal, error) {
	t, err := s.d.Trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	now := s.d.Now()
	actions, err := Extract(ctx, s.d.LLM, message, snapshotOf(t, now))
	if err != nil || len(actions) == 0 {
		return nil, err
	}
	sessionID = s.ownedSession(ctx, userID, sessionID)

	out := make([]Proposal, 0, len(actions))
	for _, a := range actions {
		// A multi-city trip cannot be re-planned from chat yet; a card that
		// could only be refused is not offered.
		if a.Kind == KindRegenerateDays && len(t.Cities) > 1 {
			continue
		}
		p := Proposal{UserID: userID, TripID: tripID, SessionID: sessionID, Action: a, ExpiresAt: now.Add(proposalTTL)}
		s.resolve(ctx, userID, &p)
		if err := s.d.Store.Create(ctx, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func snapshotOf(t *trip.Trip, now time.Time) Snapshot {
	snap := Snapshot{Days: len(t.Days), Today: now}
	for _, c := range t.Cities {
		snap.Cities = append(snap.Cities, c.CityName)
	}
	if len(snap.Cities) == 0 && t.CityName != "" {
		snap.Cities = []string{t.CityName}
	}
	if t.StartDate != nil && t.EndDate != nil {
		snap.StartDate, snap.EndDate = t.StartDate.Format(time.DateOnly), t.EndDate.Format(time.DateOnly)
	}
	return snap
}

// ownedSession keeps sessionID only if it is the caller's thread.
func (s *Service) ownedSession(ctx context.Context, userID, sessionID uuid.UUID) uuid.UUID {
	if sessionID == uuid.Nil || s.d.Sessions == nil {
		return uuid.Nil
	}
	sess, err := s.d.Sessions.GetSession(ctx, sessionID)
	if err != nil || sess == nil || sess.UserID != userID {
		return uuid.Nil
	}
	return sessionID
}

// resolve writes the card's summary and, for pick-one actions, its options.
// A lookup that fails leaves a card that says so rather than failing the turn.
func (s *Service) resolve(ctx context.Context, userID uuid.UUID, p *Proposal) {
	a := p.Action
	switch a.Kind {
	case KindSetDates:
		p.Summary = "Set the trip's dates to " + dayRange(a.StartDate, a.EndDate) + "."
	case KindRegenerateDays:
		p.Summary = fmt.Sprintf("Re-plan the itinerary as %d days.", a.Days)
	case KindSearchHotels:
		opts, err := s.hotelOptions(ctx, userID, a)
		if err != nil {
			s.d.Logger.WarnContext(ctx, "trip action: hotel search failed", slog.Any("error", err))
		}
		p.Options = opts
		if len(opts) == 0 {
			p.Summary = fmt.Sprintf("No %s hotels found near %s.", starsLabel(a), a.City)
			return
		}
		p.Summary = fmt.Sprintf("%s hotels in %s: pick one to stay at.", starsLabel(a), a.City)
	case KindSearchFlights:
		f, err := s.flightOption(a)
		if err != nil {
			s.d.Logger.WarnContext(ctx, "trip action: flight links failed", slog.Any("error", err))
			p.Summary = fmt.Sprintf("Couldn't build a flight search for %s → %s.", a.Origin.Name, a.Destination.Name)
			return
		}
		p.Options = []Option{f}
		p.Summary = fmt.Sprintf("Flights %s → %s, %s, %d traveller(s): save this search to the trip.",
			a.Origin.Name, a.Destination.Name, flightDates(a), a.Passengers)
	}
}

func starsLabel(a Action) string {
	switch {
	case a.MinStars == 0 && a.MaxStars == 5:
		return "any-star"
	case a.MinStars == a.MaxStars:
		return fmt.Sprintf("%d★", a.MinStars)
	default:
		return fmt.Sprintf("%d–%d★", a.MinStars, a.MaxStars)
	}
}

func (s *Service) hotelOptions(ctx context.Context, userID uuid.UUID, a Action) ([]Option, error) {
	places, err := s.d.Places.Search(ctx, a.City, 1)
	if err != nil || len(places) == 0 {
		return nil, err
	}
	// Stars are filtered here, not by the search: its own filter compares the
	// stored text for equality, so "4" never matches "4 stars" or "4.5".
	found, err := s.d.Hotels.GetNearbyHotels(ctx, userID, places[0].Lat, places[0].Lon, hotelRadiusMeters, "", "")
	if err != nil {
		return nil, err
	}
	var keep []locitypes.POIDetailedInfo
	for _, h := range found {
		if starsWithin(h.StarRating, a.MinStars, a.MaxStars) {
			keep = append(keep, h)
		}
	}
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].Rating > keep[j].Rating })
	if len(keep) > maxHotelOptions {
		keep = keep[:maxHotelOptions]
	}
	opts := make([]Option, 0, len(keep))
	for _, h := range keep {
		stars := ""
		if v, ok := starsOf(h.StarRating); ok {
			stars = strconv.FormatFloat(v, 'f', -1, 64)
		}
		stay := &trip.TripStay{CityName: a.City, Name: h.Name, StarRating: stars}
		if h.ID != uuid.Nil {
			stay.POIID = h.ID.String()
		}
		// Only an https site becomes the booking link friends can tap.
		if strings.HasPrefix(h.Website, "https://") {
			site := h.Website
			stay.BookingURL = &site
		}
		label := h.Name
		if stars != "" {
			label += " · " + stars + "★"
		}
		opts = append(opts, Option{Label: label, Detail: h.Address, Stay: stay})
	}
	return opts, nil
}

func (s *Service) flightOption(a Action) (Option, error) {
	depart, err := time.Parse(time.DateOnly, a.Depart)
	if err != nil {
		return Option{}, err
	}
	var ret *time.Time
	if a.Return != "" {
		r, err := time.Parse(time.DateOnly, a.Return)
		if err != nil {
			return Option{}, err
		}
		ret = &r
	}
	q := flights.Query{
		Origin:      flights.Place{Name: a.Origin.Name, IATA: a.Origin.IATA},
		Destination: flights.Place{Name: a.Destination.Name, IATA: a.Destination.IATA},
		Depart:      depart, Return: ret, Passengers: a.Passengers, Cabin: cabins[a.Cabin],
	}
	links, err := s.d.Plans.FlightLinks(q)
	if err != nil {
		return Option{}, err
	}
	return Option{
		Label:  "Save this flight search",
		Detail: fmt.Sprintf("%s → %s", a.Origin.Name, a.Destination.Name),
		Flight: &trip.TripFlight{
			Origin: q.Origin, Destination: q.Destination, DepartDate: depart, ReturnDate: ret,
			Passengers: int32(a.Passengers), Cabin: q.Cabin, Links: links,
		},
	}, nil
}

// Apply makes the change p proposes. The proposal is claimed before the
// change and given back if the change fails, so a double tap applies once
// and a failure can be retried.
func (s *Service) Apply(ctx context.Context, userID, proposalID uuid.UUID, option *int, baseVersion int64) (*trip.Trip, *locitypes.ConversationMessage, error) {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != StatusPending {
		return nil, nil, ErrNotPending
	}
	if !s.d.Now().Before(p.ExpiresAt) {
		return nil, nil, ErrExpired
	}
	opt, err := pick(p, option)
	if err != nil {
		return nil, nil, err
	}
	if err := s.d.Store.Transition(ctx, p.ID, StatusPending, StatusApplied); err != nil {
		return nil, nil, err
	}
	t, err := s.apply(ctx, userID, p, opt, baseVersion)
	if err != nil {
		// The likeliest failure is the context itself (the client left, or
		// the RPC deadline hit mid re-plan), so giving the proposal back
		// cannot share it: a proposal stuck as applied could never be retried.
		backCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveBackTimeout)
		defer cancel()
		if rerr := s.d.Store.Transition(backCtx, p.ID, StatusApplied, StatusPending); rerr != nil {
			s.d.Logger.WarnContext(ctx, "trip action: could not give a failed proposal back", slog.Any("error", rerr))
		}
		return nil, nil, err
	}
	return t, s.confirm(ctx, p, opt), nil
}

// ApplyCurrent applies a proposal against the trip as it is now. It is for
// surfaces whose button cannot carry the version it was shown with: a
// Telegram callback holds 64 bytes. The proposal still applies once and still
// expires; a change made in between is simply built on.
func (s *Service) ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error) {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return nil, nil, err
	}
	t, err := s.d.Trips.GetTrip(ctx, p.TripID, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.Apply(ctx, userID, proposalID, option, t.Version)
}

// pick is the option a pick-one action is applied with. Flights have one,
// taken by default; hotels need the traveller's choice.
func pick(p *Proposal, option *int) (*Option, error) {
	switch p.Action.Kind {
	case KindSearchHotels:
		if option == nil || *option < 0 || *option >= len(p.Options) {
			return nil, ErrNoOption
		}
		return &p.Options[*option], nil
	case KindSearchFlights:
		i := 0
		if option != nil {
			i = *option
		}
		if i < 0 || i >= len(p.Options) {
			return nil, ErrNoOption
		}
		return &p.Options[i], nil
	default:
		return nil, nil
	}
}

func (s *Service) apply(ctx context.Context, userID uuid.UUID, p *Proposal, opt *Option, base int64) (*trip.Trip, error) {
	a := p.Action
	switch a.Kind {
	case KindSetDates:
		start, err := time.Parse(time.DateOnly, a.StartDate)
		if err != nil {
			return nil, fmt.Errorf("%w: start date %q", trip.ErrInvalidEdit, a.StartDate)
		}
		end, err := time.Parse(time.DateOnly, a.EndDate)
		if err != nil {
			return nil, fmt.Errorf("%w: end date %q", trip.ErrInvalidEdit, a.EndDate)
		}
		return s.d.Plans.SetDates(ctx, userID, p.TripID, base, start, end)
	case KindSearchHotels:
		return s.d.Plans.SetStay(ctx, userID, p.TripID, base, *opt.Stay)
	case KindSearchFlights:
		return s.d.Plans.AddFlight(ctx, userID, p.TripID, base, *opt.Flight)
	case KindRegenerateDays:
		// Check before generating: a re-plan takes a minute of model time,
		// and a stale or multi-city trip would only be refused at the end.
		t, err := s.d.Trips.GetTrip(ctx, p.TripID, userID)
		if err != nil {
			return nil, err
		}
		if t.Version != base {
			return nil, trip.ErrVersionConflict
		}
		if len(t.Cities) > 1 {
			return nil, fmt.Errorf("%w: re-planning a multi-city trip from chat is not supported yet", trip.ErrInvalidEdit)
		}
		days, err := s.d.Regen.GenerateDays(ctx, userID, parentSession(t, p), t.CityName, a.Days)
		if err != nil {
			return nil, fmt.Errorf("re-plan %s: %w", t.CityName, err)
		}
		return s.d.Plans.ReplaceDays(ctx, userID, p.TripID, base, days)
	}
	return nil, fmt.Errorf("%w: unknown action %q", trip.ErrInvalidEdit, a.Kind)
}

// parentSession is the thread a re-plan's generation hangs under: the one
// the trip was generated in, else the one the proposal came from.
func parentSession(t *trip.Trip, p *Proposal) uuid.UUID {
	if t.SourceSessionID != nil {
		if id, err := uuid.Parse(*t.SourceSessionID); err == nil {
			return id
		}
	}
	return p.SessionID
}

// confirm builds the thread message for an applied proposal and posts it
// when the proposal came from a thread. Posting is best effort: the trip has
// already changed.
func (s *Service) confirm(ctx context.Context, p *Proposal, opt *Option) *locitypes.ConversationMessage {
	var text string
	switch p.Action.Kind {
	case KindSetDates:
		text = "Dates set: " + dayRange(p.Action.StartDate, p.Action.EndDate) + "."
	case KindSearchHotels:
		text = fmt.Sprintf("Staying at %s in %s.", opt.Stay.Name, opt.Stay.CityName)
	case KindSearchFlights:
		text = fmt.Sprintf("Flight search saved: %s → %s, %s.", p.Action.Origin.Name, p.Action.Destination.Name, flightDates(p.Action))
	case KindRegenerateDays:
		text = fmt.Sprintf("Itinerary re-planned as %d days.", p.Action.Days)
	}
	msg := locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleAssistant, Content: text, MessageType: locitypes.TypeResponse,
		Timestamp: s.d.Now(), Origin: locitypes.OriginProactive, SourceLabel: sourceLabel,
	}
	if p.SessionID != uuid.Nil && s.d.Sessions != nil {
		if err := s.d.Sessions.AddMessageToSession(ctx, p.SessionID, msg); err != nil {
			s.d.Logger.WarnContext(ctx, "trip action applied but confirmation not posted", slog.Any("error", err))
		}
	}
	return &msg
}

// Dismiss drops the caller's proposal.
func (s *Service) Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return err
	}
	return s.d.Store.Transition(ctx, p.ID, StatusPending, StatusDismissed)
}

func dayRange(start, end string) string {
	s, err1 := time.Parse(time.DateOnly, start)
	e, err2 := time.Parse(time.DateOnly, end)
	if err1 != nil || err2 != nil {
		return start + " – " + end
	}
	return s.Format("2 Jan") + " – " + e.Format("2 Jan 2006")
}

func flightDates(a Action) string {
	if a.Return == "" {
		if d, err := time.Parse(time.DateOnly, a.Depart); err == nil {
			return d.Format("2 Jan 2006") + " (one way)"
		}
		return a.Depart + " (one way)"
	}
	return dayRange(a.Depart, a.Return)
}
