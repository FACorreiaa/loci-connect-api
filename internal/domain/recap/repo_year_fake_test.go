package recap

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
)

func (f *fakeRepo) byYear(userID uuid.UUID, year int) *Row {
	for _, r := range f.rows {
		if r.Kind == KindYear && r.UserID == userID && r.Year == year {
			return r
		}
	}
	return nil
}

func (f *fakeRepo) YearCandidates(_ context.Context, year int, only []uuid.UUID, _ int) ([]YearCandidate, error) {
	f.yearOnlyIn = append(f.yearOnlyIn, only)
	var out []YearCandidate
	for _, c := range f.yearCands {
		if f.byYear(c.UserID, year) != nil || (only != nil && !slices.Contains(only, c.UserID)) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeRepo) QueueYear(_ context.Context, userID uuid.UUID, year int) (bool, error) {
	if f.byYear(userID, year) != nil {
		return false, nil
	}
	start, end := yearPeriod(year)
	id := uuid.New()
	f.rows[id] = &Row{ID: id, UserID: userID, Kind: KindYear, Year: year, PeriodStart: start, PeriodEnd: end, Status: StatusPending}
	return true, nil
}

func (f *fakeRepo) UpsertYear(_ context.Context, userID uuid.UUID, year int, status Status, payload []byte, reason string) (Row, error) {
	r := f.byYear(userID, year)
	if r == nil {
		id := uuid.New()
		start, end := yearPeriod(year)
		r = &Row{ID: id, UserID: userID, Kind: KindYear, Year: year, PeriodStart: start, PeriodEnd: end}
		f.rows[id] = r
	} else if r.ShareCode != "" {
		return *r, nil
	}
	r.Status, r.Payload, r.IneligibleReason = status, payload, reason
	if status == StatusReady && r.ReadyAt == nil {
		r.ReadyAt = ptr(time.Now())
	}
	if r.NotifiedAt == nil {
		r.NotifiedAt = ptr(time.Now())
	}
	return *r, nil
}

func (f *fakeRepo) ByYear(_ context.Context, userID uuid.UUID, year int) (Row, error) {
	if r := f.byYear(userID, year); r != nil {
		return *r, nil
	}
	return Row{}, ErrNotFound
}

func (f *fakeRepo) Timezone(context.Context, uuid.UUID) (string, error) { return f.tz, nil }

func (f *fakeRepo) LoadYearFacts(_ context.Context, userID uuid.UUID, year int, _ time.Time) (*YearFacts, error) {
	f.yearLoads++
	yf, ok := f.yearFacts[userID]
	if !ok {
		return &YearFacts{UserID: userID, Year: year}, nil
	}
	cp := *yf
	cp.Year = year
	return &cp, nil
}
