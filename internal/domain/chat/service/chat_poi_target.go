package service

// How many places an answer asks for.
//
// Nothing used to decide this. The itinerary and general-POI prompts carried no
// count at all, so roughly ten came back whatever was asked — the same ten for
// a weekend as for a month, which is underwhelming exactly when the traveller
// has committed the most time.
//
// The count is now days multiplied by a per-day rate and then bounded. Thinking
// in days rather than in a flat number is the point: a target is only sensible
// relative to how long someone has to spend it.
const (
	// poiTargetMin is a floor rather than a real trip's worth. A one-day
	// request still wants a morning, an afternoon, an evening and two meals.
	poiTargetMin = 8

	// The ceilings are where a list stops being a plan and becomes a catalogue.
	// They are also where generation quality falls off: a model asked for eighty
	// named places starts padding long before it gets there.
	poiTargetMaxFree = 40
	poiTargetMaxPro  = 50

	// Longer trips get a lighter day, because a fortnight is not a weekend
	// repeated seven times — there are rest days, repeat visits and travel in it.
	perDayShort = 6
	perDayLong  = 5

	// longTripThreshold is where the lighter rate starts.
	longTripThreshold = 5
)

// perDayFor is how many places one day of a trip this long is worth.
func perDayFor(days int) int {
	if days >= longTripThreshold {
		return perDayLong
	}
	return perDayShort
}

// poiTargetMaxFor is the ceiling for a plan.
func poiTargetMaxFor(pro bool) int {
	if pro {
		return poiTargetMaxPro
	}
	return poiTargetMaxFree
}

// resolvePOITarget is how many places a request of this length, on this plan,
// asks the model for.
//
// The curve: 1d→8, 2d→12, 4d→24, 5d→25, 8d→40, and a month→40 free / 50 pro.
// "4 days in Madeira" moves from ten places to twenty-four, which is the whole
// point of the exercise.
func resolvePOITarget(days int, pro bool) int {
	if days < 1 {
		days = 1
	}
	target := days * perDayFor(days)

	if maximum := poiTargetMaxFor(pro); target > maximum {
		target = maximum
	}
	if target < poiTargetMin {
		target = poiTargetMin
	}
	return target
}
