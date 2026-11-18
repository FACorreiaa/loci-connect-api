package placecheck

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// Calendar says whether a day is a public holiday at a place. It returns the
// holiday's name, or "" for an ordinary day or when it does not know.
type Calendar interface {
	Holiday(ctx context.Context, lat, lon float64, day time.Time) string
}

// Day is one day of a plan, as much of it as a check needs.
type Day struct {
	// Date is the calendar day; nil for a "Day 2" plan with no dates.
	Date *time.Time
	// Centre is the middle of the city the day is spent in, and HasCentre
	// says whether it is known. A day with no centre is answered from what
	// is already held and never looked up: there is nowhere to search.
	Centre    transit.LatLon
	HasCentre bool
	Stops     []Stop
}

// Stop is one stop of a day.
type Stop struct {
	// POIID is the place behind the stop; "" for a pasted pin or a note,
	// which have nothing to check.
	POIID string
	// Name is the place's name, the thing looked for on the map.
	Name string
	// Category is what the plan says the place is, as free text.
	Category        string
	StartMinute     *int32
	DurationMinutes *int32
}

const (
	// lookupTimeout bounds one background visit to the map.
	lookupTimeout = 45 * time.Second
	// maxLookups is how many background lookups run at once. The map server
	// is shared with stay transport and is not large.
	maxLookups = 2
)

// Planner answers for a whole plan at once.
//
// It never makes a reader wait for the map. An answer comes from what is
// already held; places not yet looked up come back unchecked and are looked
// up in the background, so the next read has them.
type Planner struct {
	svc *Service
	cal Calendar
	log *slog.Logger

	slots chan struct{}
	mu    sync.Mutex
	busy  map[string]bool
	// wg lets a test wait for the background lookups it set off.
	wg sync.WaitGroup
}

// NewPlanner builds the planner. cal may be nil: no day is then a holiday.
func NewPlanner(svc *Service, cal Calendar, log *slog.Logger) *Planner {
	if log == nil {
		log = slog.Default()
	}
	return &Planner{svc: svc, cal: cal, log: log, slots: make(chan struct{}, maxLookups), busy: map[string]bool{}}
}

// Check answers for every stop of every day, in the order given: out[i][j]
// is day i's stop j. A stop with no place behind it gets the zero Check.
//
// It does not fail. If the store cannot be read every stop is unchecked,
// which a client shows as no stamp at all.
func (p *Planner) Check(ctx context.Context, days []Day) [][]Check {
	out := make([][]Check, len(days))
	var ids []string
	for i, d := range days {
		out[i] = make([]Check, len(d.Stops))
		for _, s := range d.Stops {
			if s.POIID != "" {
				ids = append(ids, s.POIID)
			}
		}
	}
	if len(ids) == 0 {
		return out
	}

	held, err := p.svc.store.Get(ctx, ids)
	if err != nil {
		p.log.WarnContext(ctx, "placecheck: reading held checks failed", slog.Any("error", err))
		return out
	}
	now := p.svc.now().UTC()

	for i, d := range days {
		holiday := ""
		if d.Date != nil && d.HasCentre && p.cal != nil {
			holiday = p.cal.Holiday(ctx, d.Centre.Lat, d.Centre.Lon, *d.Date)
		}
		var due []POI
		for j, s := range d.Stops {
			if s.POIID == "" {
				continue
			}
			v, ok := held[s.POIID]
			if !ok || !v.Fresh(now) {
				// A stale answer is still shown; it is refreshed behind it.
				if name := strings.TrimSpace(s.Name); name != "" {
					due = append(due, POI{ID: s.POIID, Name: name, Category: s.Category})
				}
			}
			if !ok {
				continue
			}
			out[i][j] = Evaluate(&v, Visit{
				Date: d.Date, StartMinute: s.StartMinute, DurationMinutes: s.DurationMinutes, Holiday: holiday,
			})
		}
		if d.HasCentre && len(due) > 0 {
			p.lookUp(d.Centre, due)
		}
	}
	return out
}

// lookUp asks the map about the places in the background. Places already
// being asked about are left out, and when every slot is taken the request
// is dropped: the next read of the plan asks again.
func (p *Planner) lookUp(near transit.LatLon, pois []POI) {
	p.mu.Lock()
	todo := make([]POI, 0, len(pois))
	for _, poi := range pois {
		if !p.busy[poi.ID] {
			todo = append(todo, poi)
		}
	}
	if len(todo) == 0 {
		p.mu.Unlock()
		return
	}
	select {
	case p.slots <- struct{}{}:
	default:
		p.mu.Unlock()
		return
	}
	for _, poi := range todo {
		p.busy[poi.ID] = true
	}
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			for _, poi := range todo {
				delete(p.busy, poi.ID)
			}
			p.mu.Unlock()
			<-p.slots
		}()
		// The reader's request is over long before the map answers.
		ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
		defer cancel()
		if _, err := p.svc.CheckPOIs(ctx, near, todo); err != nil {
			p.log.WarnContext(ctx, "placecheck: background lookup failed",
				slog.Int("places", len(todo)), slog.Any("error", err))
		}
	}()
}

// Wait blocks until the background lookups already set off have finished.
// For tests and for a clean shutdown.
func (p *Planner) Wait() { p.wg.Wait() }
