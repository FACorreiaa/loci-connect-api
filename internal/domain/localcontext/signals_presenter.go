package localcontext

import (
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// alertKindToProto maps a domain alert kind onto the wire enum.
//
// Every kind the domain produces now has a wire value (proto v5.1.1). An
// unrecognised kind still falls through to UNSPECIFIED rather than being
// dropped: a traveller is better served by an alert they can read with a vague
// category than by silence.
func alertKindToProto(k AlertKind) lcv1.AlertKind {
	switch k {
	case AlertClosure:
		return lcv1.AlertKind_ALERT_KIND_CLOSURE
	case AlertHoliday:
		return lcv1.AlertKind_ALERT_KIND_HOLIDAY
	case AlertStrike:
		return lcv1.AlertKind_ALERT_KIND_STRIKE
	case AlertHazard:
		return lcv1.AlertKind_ALERT_KIND_HAZARD
	case AlertAirQuality:
		return lcv1.AlertKind_ALERT_KIND_AIR_QUALITY
	case AlertTransit:
		return lcv1.AlertKind_ALERT_KIND_TRANSIT
	case AlertAdvisory:
		return lcv1.AlertKind_ALERT_KIND_ADVISORY
	default:
		return lcv1.AlertKind_ALERT_KIND_UNSPECIFIED
	}
}

// ToLocalAlertsProto converts gathered alerts for the wire.
func ToLocalAlertsProto(alerts []Alert) []*lcv1.LocalAlert {
	if len(alerts) == 0 {
		return nil
	}
	out := make([]*lcv1.LocalAlert, 0, len(alerts))
	for _, a := range alerts {
		// LocalAlert.title carries min_len: 1, so an alert without one would be
		// rejected by the outbound validator and fail the whole RPC. Skipping it
		// keeps one malformed upstream row from blanking every other alert.
		if a.Title == "" {
			continue
		}
		pa := &lcv1.LocalAlert{
			Kind:   alertKindToProto(a.Kind),
			Title:  truncate(a.Title, 300),
			Detail: truncate(a.Detail, 1000),
			// effectiveSeverity, not the raw field: the domain treats zero as
			// "unspecified, full weight", and sending a literal 0 would tell the
			// client the opposite of what the scorer used.
			Severity: float64(effectiveSeverity(a)),
			Source:   truncate(a.Source, 50),
		}
		if a.Date != nil {
			pa.Date = timestamppb.New(*a.Date)
		}
		// Both or neither. A half-located alert would be drawn at the equator,
		// and the proto marks them optional precisely so country-scoped alerts
		// can leave them out.
		if a.Located() {
			lat, lon := *a.Lat, *a.Lon
			pa.Latitude, pa.Longitude = &lat, &lon
		}
		out = append(out, pa)
	}
	return out
}

// truncate keeps a string inside the proto's declared max_len. The validator
// rejects an over-long field, so a chatty upstream title would otherwise take
// the whole response down.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
