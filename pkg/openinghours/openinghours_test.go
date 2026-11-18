package openinghours

import (
	"errors"
	"testing"
	"time"
)

func mins(h, m int) int { return h*60 + m }

func TestEval(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wd      time.Weekday
		start   int
		dur     int
		holiday bool
		want    Result
	}{
		{
			name: "open inside a plain weekday range",
			raw:  "Mo-Fr 09:00-17:00", wd: time.Tuesday, start: mins(10, 0), dur: 60,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(17, 0)},
		},
		{
			name: "a day no rule names is closed",
			raw:  "Mo-Fr 09:00-17:00", wd: time.Saturday, start: mins(10, 0), dur: 60,
			want: Result{State: ClosedAllDay},
		},
		{
			name: "arriving before opening",
			raw:  "Tu-Su 10:00-18:00", wd: time.Wednesday, start: mins(9, 30), dur: 90,
			want: Result{State: OpensLater, Opens: mins(10, 0), Closes: mins(18, 0)},
		},
		{
			name: "the visit runs past closing",
			raw:  "Tu-Su 10:00-18:00", wd: time.Wednesday, start: mins(17, 30), dur: 90,
			want: Result{State: ClosesEarly, Opens: mins(10, 0), Closes: mins(18, 0)},
		},
		{
			name: "arriving after the last closing",
			raw:  "Tu-Su 10:00-18:00", wd: time.Wednesday, start: mins(19, 0), dur: 30,
			want: Result{State: ClosedByThen},
		},
		{
			name: "explicit off for one day",
			raw:  "Mo off; Tu-Su 10:00-18:00", wd: time.Monday, start: mins(11, 0), dur: 60,
			want: Result{State: ClosedAllDay},
		},
		{
			name: "a later rule overrides an earlier one for its days",
			raw:  "Mo-Su 10:00-18:00; Mo closed", wd: time.Monday, start: mins(11, 0), dur: 60,
			want: Result{State: ClosedAllDay},
		},
		{
			name: "a later rule replaces the hours, it does not add to them",
			raw:  "Mo-Su 10:00-18:00; Sa 10:00-14:00", wd: time.Saturday, start: mins(15, 0), dur: 30,
			want: Result{State: ClosedByThen},
		},
		{
			name: "lunch break: arriving in the gap",
			raw:  "Mo-Sa 09:00-13:00,15:00-19:00", wd: time.Thursday, start: mins(13, 30), dur: 60,
			want: Result{State: OpensLater, Opens: mins(15, 0), Closes: mins(19, 0)},
		},
		{
			name: "lunch break: second span",
			raw:  "Mo-Sa 09:00-13:00, 15:00-19:00", wd: time.Thursday, start: mins(16, 0), dur: 60,
			want: Result{State: Open, Opens: mins(15, 0), Closes: mins(19, 0)},
		},
		{
			name: "comma starts an additional rule when a day follows",
			raw:  "Mo-Fr 10:00-12:00, Sa 10:00-14:00", wd: time.Saturday, start: mins(13, 0), dur: 30,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(14, 0)},
		},
		{
			name: "an additional rule adds hours to a day already covered",
			raw:  "Mo-Fr 09:00-12:00, We 14:00-18:00", wd: time.Wednesday, start: mins(10, 0), dur: 30,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(12, 0)},
		},
		{
			name: "day lists",
			raw:  "Mo,We,Fr 08:00-12:00", wd: time.Wednesday, start: mins(8, 0), dur: 30,
			want: Result{State: Open, Opens: mins(8, 0), Closes: mins(12, 0)},
		},
		{
			name: "a wrapping day range",
			raw:  "Fr-Mo 10:00-16:00", wd: time.Sunday, start: mins(11, 0), dur: 30,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(16, 0)},
		},
		{
			name: "no day selector means every day",
			raw:  "10:00-24:00", wd: time.Sunday, start: mins(23, 0), dur: 60,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(24, 0)},
		},
		{
			name: "24/7",
			raw:  "24/7", wd: time.Sunday, start: mins(3, 0), dur: 60,
			want: Result{State: Open, Opens: 0, Closes: mins(24, 0)},
		},
		{
			name: "over midnight, evening side",
			raw:  "Fr-Sa 19:00-02:00", wd: time.Friday, start: mins(23, 30), dur: 90,
			want: Result{State: Open, Opens: mins(19, 0), Closes: mins(26, 0)},
		},
		{
			name: "over midnight, the small hours belong to the day before",
			raw:  "Fr-Sa 19:00-02:00", wd: time.Sunday, start: mins(0, 30), dur: 60,
			want: Result{State: Open, Opens: 0, Closes: mins(2, 0)},
		},
		{
			name: "over midnight, later on a day with no rule of its own",
			raw:  "Fr-Sa 19:00-02:00", wd: time.Sunday, start: mins(12, 0), dur: 60,
			want: Result{State: ClosedByThen},
		},
		{
			name: "PH off closes a holiday",
			raw:  "Mo-Su 10:00-18:00; PH off", wd: time.Tuesday, start: mins(11, 0), dur: 60, holiday: true,
			want: Result{State: ClosedAllDay},
		},
		{
			name: "PH off first still wins over weekday rules after it",
			raw:  "PH off; Mo-Su 10:00-18:00", wd: time.Tuesday, start: mins(11, 0), dur: 60, holiday: true,
			want: Result{State: ClosedAllDay},
		},
		{
			name: "PH off does nothing on an ordinary day",
			raw:  "Mo-Su 10:00-18:00; PH off", wd: time.Tuesday, start: mins(11, 0), dur: 60,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(18, 0)},
		},
		{
			name: "PH with its own hours",
			raw:  "Mo-Sa 09:00-19:00; PH 10:00-14:00", wd: time.Tuesday, start: mins(15, 0), dur: 30, holiday: true,
			want: Result{State: ClosedByThen},
		},
		{
			name: "PH listed beside weekdays",
			raw:  "Su,PH 10:00-13:00", wd: time.Wednesday, start: mins(11, 0), dur: 30, holiday: true,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(13, 0)},
		},
		{
			name: "a holiday the hours say nothing about is flagged, not guessed",
			raw:  "Mo-Su 10:00-18:00", wd: time.Tuesday, start: mins(11, 0), dur: 60, holiday: true,
			want: Result{State: Open, Opens: mins(10, 0), Closes: mins(18, 0), HolidayUncertain: true},
		},
		{
			name: "case and dash variants are tolerated",
			raw:  "mo-fr 09:00–17:00", wd: time.Monday, start: mins(9, 0), dur: 30,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(17, 0)},
		},
		{
			name: "the open keyword is noise",
			raw:  "Mo-Fr 09:00-17:00 open", wd: time.Monday, start: mins(9, 0), dur: 30,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(17, 0)},
		},
		{
			name: "a trailing separator is tolerated",
			raw:  "Mo-Fr 09:00-17:00;", wd: time.Monday, start: mins(9, 0), dur: 30,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(17, 0)},
		},
		{
			name: "a zero-length visit at opening time is open",
			raw:  "Mo-Fr 09:00-17:00", wd: time.Monday, start: mins(9, 0), dur: 0,
			want: Result{State: Open, Opens: mins(9, 0), Closes: mins(17, 0)},
		},
		{
			name: "arriving exactly at closing is too late",
			raw:  "Mo-Fr 09:00-17:00", wd: time.Monday, start: mins(17, 0), dur: 30,
			want: Result{State: ClosedByThen},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.raw, err)
			}
			if got := s.Eval(tt.wd, tt.start, tt.dur, tt.holiday); got != tt.want {
				t.Errorf("Eval = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Anything outside the subset must be refused: a stamp that says "open" on
// hours this package only half understood is worse than no stamp.
func TestParseRefusesWhatItDoesNotRead(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		";",
		"Apr-Oct: Mo-Su 10:00-18:00",
		"Mo-Fr 09:00-17:00; Dec 25 off",
		"week 1-53/2 Mo 10:00-12:00",
		"Mo-Fr sunrise-sunset",
		"Mo-Fr 09:00-17:00 || \"by appointment\"",
		"Mo-Fr 09:00-17:00 \"ring the bell\"",
		"Mo[1] 10:00-12:00",
		"SH off; Mo-Fr 09:00-17:00",
		"Mo-Fr 09:00+",
		"Mo-Fr 09:00-17:00; 2026 Dec 24 off",
		"Mo-Fr 9am-5pm",
		"Mo-Fr 25:00-26:00",
		"Mo-Fr 24:00-02:00",
		"Mo-Fr 09:70-17:00",
		"Mo-Fr",
		"Mo-Xx 09:00-17:00",
		"Monday 09:00-17:00",
		"unknown",
		"Mo-Fr 09:00-17:00 unknown",
		"Mo-Fr 10:00-10:00",
		"Mo 10:00-12:00 off",
		"Mo off 10:00-12:00",
		"open",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) error = %v, want ErrUnsupported", raw, err)
		}
	}
}

func TestZeroScheduleIsUnknown(t *testing.T) {
	var s Schedule
	if got := s.Eval(time.Monday, mins(10, 0), 60, false); got.State != Unknown {
		t.Errorf("zero Schedule Eval = %+v, want Unknown", got)
	}
}

func TestStateString(t *testing.T) {
	want := map[State]string{
		Unknown: "unknown", Open: "open", ClosedAllDay: "closed_all_day",
		OpensLater: "opens_later", ClosesEarly: "closes_early", ClosedByThen: "closed_by_then",
	}
	for st, s := range want {
		if st.String() != s {
			t.Errorf("State(%d).String() = %q, want %q", st, st.String(), s)
		}
	}
}

func TestOpensOn(t *testing.T) {
	late, err := Parse("Fr-Sa 19:00-02:00")
	if err != nil {
		t.Fatal(err)
	}
	// Sunday only has the tail of Saturday night: it does not open on Sunday.
	if opens, _, known := late.OpensOn(time.Sunday, false); opens || !known {
		t.Errorf("Sunday: opens=%v known=%v, want false true", opens, known)
	}
	if opens, _, _ := late.OpensOn(time.Saturday, false); !opens {
		t.Error("Saturday: want opens")
	}

	plain, _ := Parse("Mo-Su 10:00-18:00; PH off")
	if opens, uncertain, _ := plain.OpensOn(time.Tuesday, true); opens || uncertain {
		t.Errorf("holiday with PH off: opens=%v uncertain=%v", opens, uncertain)
	}
	silent, _ := Parse("Mo-Su 10:00-18:00")
	if opens, uncertain, _ := silent.OpensOn(time.Tuesday, true); !opens || !uncertain {
		t.Errorf("holiday the hours ignore: opens=%v uncertain=%v", opens, uncertain)
	}
	var zero Schedule
	if _, _, known := zero.OpensOn(time.Monday, false); known {
		t.Error("zero Schedule: want unknown")
	}
}
