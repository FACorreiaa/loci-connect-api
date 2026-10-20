package push

import (
	"context"
	"strconv"

	"github.com/google/uuid"
)

// RecapCategory is the notification category a ready trip recap carries.
const RecapCategory = "loci_recap"

// BuildRecapPayload announces a ready trip recap; it opens the recap.
func BuildRecapPayload(tripID uuid.UUID, title, body string) Payload {
	return Payload{
		Category: RecapCategory,
		ThreadID: "recap",
		Origin:   "recap_ready",
		Title:    title,
		Body:     body,
		URL:      WebOrigin + "/recap/trip/" + tripID.String(),
	}
}

// RecapReady tells `to` their trip recap is ready, unless progress updates
// are off. A recap is a summary of progress, so it shares that switch rather
// than adding one.
func (n *Notifier) RecapReady(_ context.Context, to, tripID uuid.UUID, title, body string) {
	n.notifyGated(to, BuildRecapPayload(tripID, title, body), progressAllowed)
}

// BuildYearRecapPayload announces a ready year in review; it opens the recap.
func BuildYearRecapPayload(year int, title, body string) Payload {
	return Payload{
		Category: RecapCategory,
		ThreadID: "recap",
		Origin:   "year_recap_ready",
		Title:    title,
		Body:     body,
		URL:      WebOrigin + "/recap/year/" + strconv.Itoa(year),
	}
}

// YearRecapReady tells `to` their year in review is ready, behind the same
// switch as trip recaps.
func (n *Notifier) YearRecapReady(_ context.Context, to uuid.UUID, year int, title, body string) {
	n.notifyGated(to, BuildYearRecapPayload(year, title, body), progressAllowed)
}
