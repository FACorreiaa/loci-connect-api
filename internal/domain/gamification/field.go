package gamification

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
)

// TooFewOnBoard is the board size below which the app says the board is
// thin rather than pretending it is a competition.
const TooFewOnBoard = 8

// neighborhoodLookup bounds a reverse geocode on a request path; the runner
// fills in whatever it misses.
const neighborhoodLookup = 2 * time.Second

// StopResult is what marking a stop did.
type StopResult struct {
	Status       StopStatus
	Points       int
	DayFinished  bool
	TripFinished bool
	WeekScore    int64
}

// MarkStop marks a stop of the caller's own trip done, skipped or open. A
// done stop pays once, ever; reopening it keeps what it paid. A day is
// finished when every stop is done or skipped and at least one is done, and
// finishing the last day finishes the trip.
func (s *Service) MarkStop(ctx context.Context, userID uuid.UUID, tripID, stopID string, status StopStatus, tz string) (StopResult, error) {
	if status != StopOpen && status != StopDone && status != StopSkipped {
		return StopResult{}, fmt.Errorf("%w: unknown stop status %d", ErrInvalid, status)
	}
	tid, err1 := uuid.Parse(tripID)
	sid, err2 := uuid.Parse(stopID)
	if err1 != nil || err2 != nil {
		return StopResult{}, ErrNotFound
	}
	if ValidTimezone(tz) {
		if err := s.repo.SetTimezone(ctx, userID, tz); err != nil {
			return StopResult{}, err
		}
	}
	trip, err := s.repo.Trip(ctx, userID, tid)
	if err != nil {
		return StopResult{}, err
	}
	di, si := -1, -1
	for d := range trip.Days {
		for i := range trip.Days[d].Stops {
			if trip.Days[d].Stops[i].ID == sid {
				di, si = d, i
			}
		}
	}
	if di < 0 {
		return StopResult{}, ErrNotFound
	}
	if err := s.repo.SetMark(ctx, tid, sid, status); err != nil {
		return StopResult{}, err
	}
	day := &trip.Days[di]
	stop := &day.Stops[si]
	stop.Status = status
	out := StopResult{Status: status}
	if !s.enabled {
		return out, nil
	}

	if status == StopDone {
		cityID, cityName := trip.City(*day)
		res, err := s.Award(ctx, Award{
			UserID: userID, Kind: KindStopDone, RefKey: "stop:" + sid.String(),
			Label: "Walked " + stop.Name, CityID: cityID,
		})
		if err != nil {
			return StopResult{}, err
		}
		out.Points += res.Points
		if cityID != nil {
			out.Points += s.firstCity(ctx, userID, *cityID, cityName)
		}
		if stop.POIID != "" {
			place, err := s.repo.Place(ctx, stop.POIID)
			if err != nil {
				s.log.Debug("neighborhood skipped", slog.Any("error", err))
			} else {
				out.Points += s.firstNeighborhood(ctx, userID, stop.POIID, place)
			}
		}
	}

	if day.Finished() {
		res, err := s.finishDay(ctx, userID, trip, di)
		if err != nil {
			return StopResult{}, err
		}
		out.Points += res.Points
		out.DayFinished = res.Points > 0
		out.TripFinished = res.TripCompleted
	}

	week, err := s.weekScore(ctx, userID)
	if err != nil {
		return StopResult{}, err
	}
	out.WeekScore = week
	return out, nil
}

func (s *Service) weekScore(ctx context.Context, userID uuid.UUID) (int64, error) {
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return 0, err
	}
	season := SeasonID(LocalDate(s.now(), totals.Timezone))
	scores, err := s.repo.SeasonScores(ctx, userID, uuid.Nil, []int{season})
	if err != nil {
		return 0, err
	}
	return scores[season].Score, nil
}

// firstCity awards the first walked stop or visit in a city.
func (s *Service) firstCity(ctx context.Context, userID, cityID uuid.UUID, name string) int {
	label := "New city"
	if name != "" {
		label = "New city: " + name
	}
	return s.AwardQuietly(ctx, Award{
		UserID: userID, Kind: KindNewCity, RefKey: "city:" + cityID.String(), Label: label, CityID: &cityID,
	})
}

// firstNeighborhood awards the first place reached in a neighborhood of a
// city. A place never looked up is looked up now, briefly; the runner covers
// lookups that time out.
func (s *Service) firstNeighborhood(ctx context.Context, userID uuid.UUID, poiID string, place Place) int {
	if place.CityID == nil {
		return 0
	}
	hood := place.Neighborhood
	if !place.NeighborhoodChecked && s.hoods != nil && place.HasLocation {
		lctx, cancel := context.WithTimeout(ctx, neighborhoodLookup)
		name, err := s.hoods.Neighborhood(lctx, place.Lat, place.Lon)
		cancel()
		if err != nil {
			s.log.Debug("neighborhood lookup failed", slog.Any("error", err))
			return 0
		}
		if err := s.repo.SetNeighborhood(ctx, poiID, name); err != nil {
			s.log.Debug("neighborhood not stored", slog.Any("error", err))
		}
		hood = name
	}
	slug := Slug(hood)
	if slug == "" {
		return 0
	}
	return s.AwardQuietly(ctx, Award{
		UserID: userID, Kind: KindNeighborhood,
		RefKey: "hood:" + place.CityID.String() + ":" + slug,
		Label:  "New neighborhood: " + hood, CityID: place.CityID,
	})
}

// SavedItem is a saved place as the field score sees it.
type SavedItem struct {
	ItemID      string
	ContentType string
	Name        string
	CityName    string
}

// cityOf is the city a saved item belongs to: a stored place's own city,
// else a city row named like the one it was saved with.
func (s *Service) cityOf(ctx context.Context, it SavedItem) *uuid.UUID {
	if it.ContentType == "poi" {
		if place, err := s.repo.Place(ctx, it.ItemID); err == nil && place.CityID != nil {
			return place.CityID
		}
	}
	id, err := s.repo.CityByName(ctx, it.CityName)
	if err != nil {
		s.log.Debug("saved item city unknown", slog.Any("error", err))
	}
	return id
}

// PlaceSaved awards a first save of a place (favorites.FieldScorer). The
// key is the place, so saving, removing and saving again pays once.
func (s *Service) PlaceSaved(ctx context.Context, userID uuid.UUID, it SavedItem) int {
	if !s.enabled || userID == uuid.Nil || it.ItemID == "" || it.ContentType == "itinerary" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	label := "Saved a place"
	if it.Name != "" {
		label = "Saved " + it.Name
	}
	return s.AwardQuietly(ctx, Award{
		UserID: userID, Kind: KindPlaceSaved, RefKey: "save:" + it.ContentType + ":" + it.ItemID,
		Label: label, CityID: s.cityOf(ctx, it),
	})
}

// NoteWritten awards a note in the caller's own words on a saved place,
// once per place (favorites.FieldScorer). counts reports whether the note
// qualified, even when it was already paid for.
func (s *Service) NoteWritten(ctx context.Context, userID uuid.UUID, it SavedItem, note string) (points int, counts bool) {
	if !s.enabled || userID == uuid.Nil || it.ContentType == "itinerary" {
		return 0, false
	}
	sources, err := s.repo.NoteSources(ctx, userID, it.ContentType, it.ItemID)
	if err != nil {
		s.log.Debug("note scoring skipped", slog.Any("error", err))
		return 0, false
	}
	if !NoteIsOriginal(note, sources) {
		return 0, false
	}
	label := "Wrote a note"
	if it.Name != "" {
		label = "Note on " + it.Name
	}
	return s.AwardQuietly(ctx, Award{
		UserID: userID, Kind: KindPlaceNote, RefKey: "note:" + it.ContentType + ":" + it.ItemID,
		Label: label, CityID: s.cityOf(ctx, it),
	}), true
}

// CityRank is a lifetime rank in one city.
type CityRank struct {
	CityID uuid.UUID
	Name   string
	Rank   FieldRank
	Score  int64
	Next   int64
}

// FieldProfile is the caller's field record.
type FieldProfile struct {
	Lifetime     int64
	Rank         FieldRank
	Next         int64
	Week         int64
	LastWeek     int64
	Cities       []CityRank
	SeasonID     int
	PlacesKept   int
	DaysFinished int
}

// Profile is userID's field record: lifetime score and rank, rank per city,
// and this week against last.
func (s *Service) Profile(ctx context.Context, userID uuid.UUID) (*FieldProfile, error) {
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return nil, err
	}
	season := SeasonID(LocalDate(s.now(), totals.Timezone))
	p := &FieldProfile{Lifetime: totals.TotalPoints, SeasonID: season}
	p.Rank, p.Next = RankFor(totals.TotalPoints, s.overallRanks)
	if !s.enabled {
		return p, nil
	}
	last := SeasonShift(season, -1)
	weeks, err := s.repo.SeasonScores(ctx, userID, uuid.Nil, []int{season, last})
	if err != nil {
		return nil, err
	}
	p.Week, p.LastWeek = weeks[season].Score, weeks[last].Score
	cities, err := s.repo.CityTotals(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range cities {
		r, next := RankFor(c.Score, s.cityRanks)
		p.Cities = append(p.Cities, CityRank{CityID: c.CityID, Name: c.Name, Rank: r, Score: c.Score, Next: next})
	}
	life, err := s.repo.Lifetime(ctx, userID)
	if err != nil {
		return nil, err
	}
	p.PlacesKept, p.DaysFinished = life.PlacesKept, life.DaysFinished
	return p, nil
}

// BoardRequest asks for one board.
type BoardRequest struct {
	Scope        Scope
	CityID       string
	Metric       FieldMetric
	SeasonOffset int
}

// FieldRow is one row of a board as the API shows it.
type FieldRow struct {
	Position    int
	DisplayName string
	// User is set for the viewer and their friends only.
	User  *socialv1.PublicUser
	Value int64
	IsMe  bool
	Rank  FieldRank
}

// PersonalWeek is the caller alone, this week against last.
type PersonalWeek struct {
	This, Last SeasonScore
}

// FieldBoard is one board.
type FieldBoard struct {
	Scope            Scope
	CityID           *uuid.UUID
	CityName         string
	SeasonID         int
	Top              []FieldRow
	Me               *FieldRow
	Above            *FieldRow
	Scored           int
	TooFew           bool
	FriendsAvailable bool
	Personal         *PersonalWeek
	HistoryLocked    bool
	MeHidden         bool
}

// Board is one weekly board for userID. A city board shows people who are
// not friends by display name only; nobody is ever ranked among everyone.
func (s *Service) Board(ctx context.Context, userID uuid.UUID, req BoardRequest) (*FieldBoard, error) {
	if req.Scope == 0 {
		req.Scope = ScopeCityWeek
	}
	if req.Metric == 0 {
		req.Metric = FieldOverall
	}
	if req.SeasonOffset > 0 {
		req.SeasonOffset = 0
	}
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return nil, err
	}
	current := SeasonID(LocalDate(s.now(), totals.Timezone))
	b := &FieldBoard{Scope: req.Scope, SeasonID: SeasonShift(current, req.SeasonOffset)}
	if !s.enabled {
		return b, nil
	}

	var friends []uuid.UUID
	if s.graph != nil {
		if friends, err = s.graph.FriendIDs(ctx, userID); err != nil {
			return nil, err
		}
	}
	b.FriendsAvailable = len(friends) > 0

	if req.SeasonOffset < 0 && !s.historyEntitled(ctx, userID) {
		b.HistoryLocked = true
		return b, nil
	}

	switch req.Scope {
	case ScopePersonal:
		last := SeasonShift(b.SeasonID, -1)
		weeks, err := s.repo.SeasonScores(ctx, userID, uuid.Nil, []int{b.SeasonID, last})
		if err != nil {
			return nil, err
		}
		b.Personal = &PersonalWeek{This: weeks[b.SeasonID], Last: weeks[last]}
		return b, nil

	case ScopeFriendsWeek:
		if !b.FriendsAvailable {
			return b, nil
		}
		visible, err := s.repo.Visible(ctx, friends)
		if err != nil {
			return nil, err
		}
		among := []uuid.UUID{userID}
		for _, f := range friends {
			if visible[f] {
				among = append(among, f)
			}
		}
		return s.fillBoard(ctx, b, userID, uuid.Nil, req.Metric, among, friends)

	default:
		if req.CityID != "" {
			id, err := uuid.Parse(req.CityID)
			if err != nil {
				return nil, fmt.Errorf("%w: city_id %q", ErrInvalid, req.CityID)
			}
			name, err := s.repo.CityName(ctx, id)
			if err != nil {
				return nil, err
			}
			b.CityID, b.CityName = &id, name
		} else if b.CityID, b.CityName, err = s.repo.DefaultCity(ctx, userID, LocalDate(s.now(), totals.Timezone)); err != nil {
			return nil, err
		}
		if b.CityID == nil {
			return b, nil
		}
		visible, err := s.repo.CityBoardVisible(ctx, userID)
		if err != nil {
			return nil, err
		}
		b.MeHidden = !visible
		return s.fillBoard(ctx, b, userID, *b.CityID, req.Metric, nil, friends)
	}
}

func (s *Service) historyEntitled(ctx context.Context, userID uuid.UUID) bool {
	if s.plans == nil {
		return true
	}
	plan, err := s.plans.EffectivePlan(ctx, userID)
	if err != nil {
		s.log.Debug("plan unknown; past boards stay closed", slog.Any("error", err))
		return !subscription.Gating()
	}
	return subscription.Entitled(plan)
}

func (s *Service) fillBoard(ctx context.Context, b *FieldBoard, viewer, cityID uuid.UUID, metric FieldMetric, among, friends []uuid.UUID) (*FieldBoard, error) {
	snapshot := false
	if b.SeasonID < SeasonID(LocalDate(s.now(), "UTC")) {
		closed, err := s.repo.SeasonClosed(ctx, b.SeasonID)
		if err != nil {
			return nil, err
		}
		snapshot = closed
	}
	page, err := s.repo.Board(ctx, BoardQuery{
		CityID: cityID, SeasonID: b.SeasonID, Metric: metric, Viewer: viewer, Among: among, Snapshot: snapshot,
	})
	if err != nil {
		return nil, err
	}
	b.Scored = page.Scored
	b.TooFew = page.Scored < TooFewOnBoard

	rows := append([]BoardRow{}, page.Top...)
	if page.Above != nil {
		rows = append(rows, *page.Above)
	}
	if page.Me != nil {
		rows = append(rows, *page.Me)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	cards := map[uuid.UUID]*socialv1.PublicUser{}
	if s.graph != nil && len(ids) > 0 {
		if cards, err = s.graph.PublicUsers(ctx, ids); err != nil {
			return nil, err
		}
	}
	lifetime, err := s.repo.LifetimeScores(ctx, cityID, ids)
	if err != nil {
		return nil, err
	}
	thresholds := s.cityRanks
	if cityID == uuid.Nil {
		thresholds = s.overallRanks
	}
	isFriend := map[uuid.UUID]bool{}
	for _, f := range friends {
		isFriend[f] = true
	}
	shape := func(r BoardRow) FieldRow {
		rank, _ := RankFor(lifetime[r.UserID], thresholds)
		row := FieldRow{Position: r.Position, Value: r.Value, IsMe: r.UserID == viewer, Rank: rank, DisplayName: "A traveller"}
		if c := cards[r.UserID]; c != nil {
			if c.GetDisplayName() != "" {
				row.DisplayName = c.GetDisplayName()
			}
			if row.IsMe || isFriend[r.UserID] {
				row.User = c
			}
		}
		return row
	}
	for _, r := range page.Top {
		b.Top = append(b.Top, shape(r))
	}
	if page.Above != nil {
		above := shape(*page.Above)
		b.Above = &above
	}
	if page.Me != nil {
		me := shape(*page.Me)
		b.Me = &me
	}
	return b, nil
}

// CloseSeasons snapshots every season whose last time zone has finished it.
func (s *Service) CloseSeasons(ctx context.Context) (int, error) {
	now := s.now()
	open, err := s.repo.OpenSeasons(ctx, SeasonID(LocalDate(now, "UTC")))
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range open {
		if !SeasonOver(id, now) {
			continue
		}
		if err := s.repo.CloseSeason(ctx, id); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// PayKeeps awards places still saved KeepAfter after their first save.
func (s *Service) PayKeeps(ctx context.Context, limit int) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	due, err := s.repo.DueKeeps(ctx, limit)
	if err != nil {
		return 0, err
	}
	paid := 0
	for _, k := range due {
		label := "Kept a place"
		if k.Name != "" {
			label = "Kept " + k.Name
		}
		if s.AwardQuietly(ctx, Award{
			UserID: k.UserID, Kind: KindPlaceKept, RefKey: "kept:" + k.ContentType + ":" + k.ItemID,
			Label: label, CityID: k.CityID,
		}) > 0 {
			paid++
		}
	}
	return paid, nil
}

// BackfillNeighborhoods looks up places on trips or visited that were never
// looked up, one every `pause`. Returns how many it looked up.
func (s *Service) BackfillNeighborhoods(ctx context.Context, limit int, pause time.Duration) (int, error) {
	if s.hoods == nil {
		return 0, nil
	}
	places, err := s.repo.PlacesWithoutNeighborhood(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for i, p := range places {
		if i > 0 && pause > 0 {
			select {
			case <-ctx.Done():
				return done, nil
			case <-time.After(pause):
			}
		}
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		name, err := s.hoods.Neighborhood(lctx, p.Lat, p.Lon)
		cancel()
		if err != nil {
			// Left unchecked: the next tick tries again.
			s.log.Debug("neighborhood backfill lookup failed", slog.String("poi_id", p.ID), slog.Any("error", err))
			continue
		}
		if err := s.repo.SetNeighborhood(ctx, p.ID, name); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}
