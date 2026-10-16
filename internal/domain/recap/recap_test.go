package recap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func TestCandidateDue(t *testing.T) {
	// 2026-10-16 12:00 UTC.
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		c    Candidate
		due  bool
		s, e string
	}{
		{"ended yesterday", Candidate{StartDate: ptr(day("2026-10-10")), EndDate: ptr(day("2026-10-15"))}, true, "2026-10-10", "2026-10-15"},
		{"ends today: not yet", Candidate{EndDate: ptr(day("2026-10-16"))}, false, "", ""},
		{"ends in the future", Candidate{EndDate: ptr(day("2026-11-01"))}, false, "", ""},
		{"ended 14 days ago: still due", Candidate{EndDate: ptr(day("2026-10-02"))}, true, "2026-10-02", "2026-10-02"},
		{"ended 15 days ago: too old to push", Candidate{EndDate: ptr(day("2026-10-01"))}, false, "", ""},
		{"no dates, days dated", Candidate{
			FirstDay: ptr(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)),
			LastDay:  ptr(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)),
		}, true, "2026-10-12", "2026-10-14"},
		{"undated, nothing walked", Candidate{Days: 3}, false, "", ""},
		{"every day walked before the end date", Candidate{
			StartDate: ptr(day("2026-10-14")), EndDate: ptr(day("2026-10-20")),
			Days: 2, DaysDone: 2, LastCompletedAt: ptr(now.Add(-time.Hour)),
		}, true, "2026-10-14", "2026-10-20"},
		{"some days walked: not over", Candidate{
			EndDate: ptr(day("2026-10-20")), Days: 3, DaysDone: 2, LastCompletedAt: ptr(now),
		}, false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, e, due := tc.c.Due(now)
			assert.Equal(t, tc.due, due)
			if tc.due {
				assert.Equal(t, tc.s, s.Format(time.DateOnly))
				assert.Equal(t, tc.e, e.Format(time.DateOnly))
			}
		})
	}
}

func TestCandidateDueUsesLocalDay(t *testing.T) {
	// 23:30 UTC on the 15th is already the 16th in Tokyo: a trip ending on
	// the 15th is over there, but not yet in Los Angeles (16:30 on the 15th).
	now := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	c := Candidate{EndDate: ptr(day("2026-10-15"))}

	c.Timezone = "Asia/Tokyo"
	_, _, due := c.Due(now)
	assert.True(t, due)

	c.Timezone = "America/Los_Angeles"
	_, _, due = c.Due(now)
	assert.False(t, due)

	c.Timezone = "Not/AZone"
	_, _, due = c.Due(now)
	assert.False(t, due, "an unknown zone falls back to UTC, where the 15th is today")
}

func TestCandidateEnded(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	_, _, ended := Candidate{EndDate: ptr(day("2025-01-01"))}.Ended(now)
	assert.True(t, ended, "on request, an old trip still gets its recap")

	_, _, ended = Candidate{}.Ended(now)
	assert.False(t, ended, "no dates and nothing walked")

	s, e, ended := Candidate{Days: 1, DaysDone: 1, LastCompletedAt: ptr(now)}.Ended(now)
	assert.True(t, ended, "an undated trip walked to the end")
	assert.Equal(t, "2026-10-16", s.Format(time.DateOnly))
	assert.Equal(t, "2026-10-16", e.Format(time.DateOnly))
}

func TestPeriodStartAfterEndIsClamped(t *testing.T) {
	s, e, ok := Candidate{StartDate: ptr(day("2026-10-20")), EndDate: ptr(day("2026-10-10"))}.Period(time.UTC)
	assert.True(t, ok)
	assert.Equal(t, s, e)
}

func TestInPushWindow(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 16, h, 0, 0, 0, time.UTC) }
	assert.False(t, InPushWindow(at(9), "UTC"))
	assert.True(t, InPushWindow(at(10), "UTC"))
	assert.True(t, InPushWindow(at(20), "UTC"))
	assert.False(t, InPushWindow(at(21), "UTC"))
	// 09:00 UTC is 11:00 in Lisbon summer time.
	assert.True(t, InPushWindow(time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC), "Europe/Lisbon"))
}

func TestRowRefreshable(t *testing.T) {
	now := time.Now()
	ready := now.Add(-time.Hour)
	old := now.Add(-RefreshWindow - time.Hour)

	assert.True(t, (&Row{Status: StatusIneligible}).Refreshable(now))
	assert.True(t, (&Row{Status: StatusReady, ReadyAt: &ready}).Refreshable(now))
	assert.False(t, (&Row{Status: StatusReady, ReadyAt: &old}).Refreshable(now))
	assert.False(t, (&Row{Status: StatusReady, ReadyAt: &ready, ShareCode: "x"}).Refreshable(now))
}
