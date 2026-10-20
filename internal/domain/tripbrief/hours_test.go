package tripbrief

import (
	"testing"
	"time"
)

func TestClosedOn(t *testing.T) {
	cases := []struct {
		name          string
		hours         string
		day           time.Weekday
		closed, known bool
	}{
		{"canonical closed day", "mon-fri 09:00-17:00; sun closed", time.Sunday, true, true},
		{"canonical open day", "mon-fri 09:00-17:00; sun closed", time.Monday, false, true},
		{"day in a range", "tue-sat 10:00-18:00; sun-mon closed", time.Monday, true, true},
		{"unlisted day is unknown", "mon-fri 09:00-17:00", time.Saturday, false, false},
		{"list of days", "mon,wed 09:00-12:00; tue closed", time.Tuesday, true, true},
		{"json string", `"mon-fri 09:00-17:00; sat closed"`, time.Saturday, true, true},
		{"json object", `{"monday":"Closed","tuesday":"09:00-17:00"}`, time.Monday, true, true},
		{"json object open", `{"monday":"Closed","tuesday":"09:00-17:00"}`, time.Tuesday, false, true},
		{"free text", "24/7", time.Sunday, false, false},
		{"empty", "", time.Sunday, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			closed, known := ClosedOn(c.hours, c.day)
			if closed != c.closed || known != c.known {
				t.Fatalf("ClosedOn(%q, %v) = (%v, %v), want (%v, %v)", c.hours, c.day, closed, known, c.closed, c.known)
			}
		})
	}
}
