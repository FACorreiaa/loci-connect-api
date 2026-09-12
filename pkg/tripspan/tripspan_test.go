package tripspan

import (
	"testing"
	"time"
)

// now is fixed so that bare date ranges ("3-9 May") do not change meaning in
// December.
var now = time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)

func TestParseAt(t *testing.T) {
	cases := []struct {
		in      string
		days    int
		source  Source
		clamped bool
	}{
		// Explicit numbers.
		{"4 days in Madeira", 4, SourceExplicitDays, false},
		{"three days in Funchal", 3, SourceExplicitDays, false},
		{"3-day trip", 3, SourceExplicitDays, false},
		{"5 nights in Porto", 5, SourceExplicitDays, false},
		{"48 hours in Lisbon", 2, SourceExplicitHours, false},
		{"36 hours in Porto", 2, SourceExplicitHours, false},
		{"a week in Madeira", 7, SourceExplicitWeeks, false},
		{"2 weeks in Sicily", 14, SourceExplicitWeeks, false},
		{"a month in Madeira", 30, SourceExplicitMonths, false},

		// Clamping keeps the source: understood, then capped.
		{"6 months in Madeira", 30, SourceExplicitMonths, true},
		{"1000 days", 30, SourceExplicitDays, true},

		// Weak phrases.
		{"a weekend in Madeira", 2, SourcePhrase, false},
		{"long weekend in Madeira", 3, SourcePhrase, false},
		{"a city break in Porto", 3, SourcePhrase, false},
		{"fortnight in Crete", 14, SourcePhrase, false},
		{"day trip to Sintra", 1, SourcePhrase, false},

		// Ranges, which beat any duration in the same sentence.
		{"Madeira 3-9 May 2026", 7, SourceDateRange, false},
		{"Madeira 2026-05-03 to 2026-05-09", 7, SourceDateRange, false},
		{"5 days in Madeira, 3-9 May", 7, SourceDateRange, false},
		{"Madeira 3 May to 9 May 2026", 7, SourceDateRange, false},
		{"Madeira May 3-9", 7, SourceDateRange, false},
		{"Madeira 3/5/2026 to 9/5/2026", 7, SourceDateRange, false},
		{"Madeira 28 Dec to 3 Jan", 7, SourceDateRange, false},
		{"Lisbon 1 May to 30 June", 30, SourceDateRange, true},

		// A backwards range with no year is a typo, not an eleven-month trip.
		// It falls through to the default.
		{"Madeira 9 May to 3 May", 2, SourceDefault, false},

		// Misses.
		{"things to do in Madeira", 2, SourceDefault, false},
		{"", 2, SourceDefault, false},
		{"   ", 2, SourceDefault, false},
		{"Madeira", 2, SourceDefault, false},
		// A bare number is never a duration.
		{"Room 101 in Madeira", 2, SourceDefault, false},
		{"top 3 beaches in Madeira", 2, SourceDefault, false},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := ParseAt(now, c.in)
			if got.Days != c.days {
				t.Errorf("Days = %d, want %d (source %q, match %q)", got.Days, c.days, got.Source, got.Match)
			}
			if got.Source != c.source {
				t.Errorf("Source = %q, want %q (match %q)", got.Source, c.source, got.Match)
			}
			if got.Clamped != c.clamped {
				t.Errorf("Clamped = %v, want %v", got.Clamped, c.clamped)
			}
			if got.Parsed() != (c.source != SourceDefault) {
				t.Errorf("Parsed() = %v, want %v", got.Parsed(), c.source != SourceDefault)
			}
		})
	}
}

// "long weekend" must not be read as "weekend". The table above covers it, but
// the ordering of the phrase list is the kind of thing a later edit reshuffles
// without noticing, so it gets its own name in the output.
func TestLongestPhraseWins(t *testing.T) {
	if got := ParseAt(now, "a long weekend in Madeira"); got.Days != 3 {
		t.Fatalf("Days = %d, want 3 (match %q)", got.Days, got.Match)
	}
}

// The year a bare range lands in comes from the caller's clock, and a range
// that has already passed this year still reads as the same number of days.
func TestBareRangeUsesSuppliedNow(t *testing.T) {
	for _, at := range []time.Time{
		time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.December, 30, 0, 0, 0, 0, time.UTC),
	} {
		if got := ParseAt(at, "Madeira 3-9 May"); got.Days != 7 || got.Source != SourceDateRange {
			t.Errorf("at %s: got %d days from %q, want 7 from date_range", at.Format("Jan 2"), got.Days, got.Source)
		}
	}
}

// Parse is the entry point a public Telegram bot feeds arbitrary text into, so
// the only real contract is that it never panics and never returns a horizon
// the rest of the system cannot size a prompt from.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"4 days in Madeira", "a month", "3-9 May", "", "0 days", "-1 days",
		"99999999999999999999 days", "May 31-1", "2026-02-30 to 2026-02-31",
		"1/1 to 1/1", "a a a a", "\x00\xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Parse(in)
		if got.Days < 1 || got.Days > MaxDays {
			t.Fatalf("Days = %d out of [1,%d] for %q", got.Days, MaxDays, in)
		}
		if got.Source == SourceDefault && got.Days != DefaultDays {
			t.Fatalf("default source but Days = %d for %q", got.Days, in)
		}
	})
}
