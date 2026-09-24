package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPropose(t *testing.T) {
	// Wednesday 2026-09-23 10:30 UTC.
	now := time.Date(2026, 9, 23, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		text     string
		interval int
		schedule string
		spec     string
		title    string
		firstRun string // RFC3339 in UTC, "" for none
	}{
		{
			name:     "morning with explicit hour",
			text:     "Every morning at 8 tell me if it will rain in Lisbon",
			interval: 1440, schedule: "Every day at 08:00",
			spec: "tell me if it will rain in Lisbon", title: "Tell me if it will rain in Lisbon",
			firstRun: "2026-09-24T08:00:00Z",
		},
		{
			name:     "daily with pm and minutes, later today",
			text:     "daily at 6:45pm check whether the Alfama fado houses have tables tonight",
			interval: 1440, schedule: "Every day at 18:45",
			spec:     "check whether the Alfama fado houses have tables tonight",
			title:    "Check whether the Alfama fado houses have tables tonight",
			firstRun: "2026-09-23T18:45:00Z",
		},
		{
			name:     "every n hours has no time of day",
			text:     "every 3 hours, watch for strikes on the Lisbon metro",
			interval: 180, schedule: "Every 3 hours",
			spec: "watch for strikes on the Lisbon metro", title: "Watch for strikes on the Lisbon metro",
		},
		{
			name:     "hourly",
			text:     "Please hourly check ferry delays to Cacilhas",
			interval: 60, schedule: "Every hour",
			spec: "check ferry delays to Cacilhas", title: "Check ferry delays to Cacilhas",
		},
		{
			name:     "weekday",
			text:     "every Friday at 9am suggest a weekend day trip from Porto",
			interval: 10080, schedule: "Every Friday at 09:00",
			spec: "suggest a weekend day trip from Porto", title: "Suggest a weekend day trip from Porto",
			firstRun: "2026-09-25T09:00:00Z",
		},
		{
			name:     "no schedule defaults to daily 09:00",
			text:     "Keep an eye on new rooftop bars in Madrid",
			interval: 1440, schedule: "Every day at 09:00",
			spec: "Keep an eye on new rooftop bars in Madrid", title: "Keep an eye on new rooftop bars in Madrid",
			firstRun: "2026-09-24T09:00:00Z",
		},
		{
			name:     "minutes are clamped to hourly",
			text:     "every 5 minutes check the Sagrada Familia queue",
			interval: 60, schedule: "Every hour",
			spec: "check the Sagrada Familia queue", title: "Check the Sagrada Familia queue",
		},
		{
			name:     "weekly",
			text:     "weekly: round up free museum days in Paris",
			interval: 10080, schedule: "Every week at 09:00",
			spec: "round up free museum days in Paris", title: "Round up free museum days in Paris",
			firstRun: "2026-09-24T09:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Propose(tt.text, time.UTC, now)
			require.NoError(t, err)
			require.Equal(t, tt.interval, p.IntervalMinutes)
			require.Equal(t, tt.schedule, p.ScheduleHuman)
			require.Equal(t, tt.spec, p.Spec)
			require.Equal(t, tt.title, p.Title)
			if tt.firstRun == "" {
				require.Nil(t, p.FirstRunAt)
				return
			}
			require.NotNil(t, p.FirstRunAt)
			require.Equal(t, tt.firstRun, p.FirstRunAt.UTC().Format(time.RFC3339))
		})
	}
}

func TestProposeUsesTheUsersZone(t *testing.T) {
	lisbon, err := time.LoadLocation("Europe/Lisbon") // UTC+1 in September
	require.NoError(t, err)
	now := time.Date(2026, 9, 23, 10, 30, 0, 0, time.UTC)

	p, err := Propose("every day at 8 check the surf at Ericeira", lisbon, now)
	require.NoError(t, err)
	require.Equal(t, "2026-09-24T07:00:00Z", p.FirstRunAt.UTC().Format(time.RFC3339))
}

func TestProposeRejectsScheduleWithoutTask(t *testing.T) {
	now := time.Now()
	_, err := Propose("every day at 8", time.UTC, now)
	require.True(t, errors.Is(err, ErrInvalid))
	_, err = Propose("   ", time.UTC, now)
	require.True(t, errors.Is(err, ErrInvalid))
}

func TestProposeLongTextTitleIsTruncatedAtAWord(t *testing.T) {
	p, err := Propose("every day tell me about every single new exhibition opening in the whole of Berlin this season", time.UTC, time.Now())
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(p.Title)), maxTitleRunes+1)
	require.Contains(t, p.Title, "…")
	require.Equal(t, "tell me about every single new exhibition opening in the whole of Berlin this season", p.Spec)
}

func TestNextSlotSkipsMissedRuns(t *testing.T) {
	base := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// On time: one interval on.
	require.Equal(t, base.Add(day), nextSlot(base, day, base.Add(time.Minute)))
	// Down for three days: the next future slot, not three catch-up runs.
	require.Equal(t, base.Add(4*day), nextSlot(base, day, base.Add(3*day+time.Hour)))
	// Exactly on a slot boundary: strictly after now.
	require.Equal(t, base.Add(3*day), nextSlot(base, day, base.Add(2*day)))
}
