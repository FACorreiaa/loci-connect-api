package handler

import (
	"testing"

	userpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user"
)

// The proto pattern checks that a timezone looks like a zone name. Only tzdata
// knows whether one exists, and the difference shows up late: an unresolvable
// zone is a scheduled notification that never fires, at 9pm, with nothing in
// the logs pointing at the profile that caused it.
func TestValidateTimezone(t *testing.T) {
	str := func(s string) *string { return &s }

	t.Run("accepts a real zone", func(t *testing.T) {
		for _, tz := range []string{"Atlantic/Madeira", "Europe/Lisbon", "UTC", "America/New_York"} {
			if err := validateTimezone(str(tz)); err != nil {
				t.Errorf("validateTimezone(%q) = %v, want nil", tz, err)
			}
		}
	})

	// A plausible misspelling passes the proto pattern and resolves to nothing.
	t.Run("refuses a zone that does not exist", func(t *testing.T) {
		for _, tz := range []string{"Atlantic/Madiera", "Europe/Lisboa", "Mars/Olympus"} {
			if err := validateTimezone(str(tz)); err == nil {
				t.Errorf("validateTimezone(%q) = nil, want an error", tz)
			}
		}
	})

	// Nil is a partial update that does not mention the timezone: leave it
	// alone. Empty is "clear it", which is how somebody goes back to letting
	// the browser guess. Neither is a validation failure.
	t.Run("nil and empty are not errors", func(t *testing.T) {
		if err := validateTimezone(nil); err != nil {
			t.Errorf("nil timezone = %v, want nil", err)
		}
		if err := validateTimezone(str("")); err != nil {
			t.Errorf("empty timezone = %v, want nil", err)
		}
	})
}

// A field the request does not set must not be written. Everything on
// UpdateProfileParams is a pointer for that reason, and a mapping that turned
// "absent" into "empty string" would silently clear a user's locale every time
// they edited their bio.
func TestFromUpdateProtoLeavesAbsentLocaleAlone(t *testing.T) {
	bio := "likes levadas"
	params := fromUpdateProto(&userpb.UpdateProfileParams{AboutYou: &bio})

	if params.Timezone != nil {
		t.Errorf("timezone = %q, want nil for a request that did not mention it", *params.Timezone)
	}
	if params.Units != nil {
		t.Errorf("units = %q, want nil", *params.Units)
	}
	if params.Currency != nil {
		t.Errorf("currency = %q, want nil", *params.Currency)
	}
}

func TestFromUpdateProtoCarriesLocale(t *testing.T) {
	tz, units, currency := "Atlantic/Madeira", "imperial", "GBP"
	params := fromUpdateProto(&userpb.UpdateProfileParams{
		Timezone: &tz,
		Units:    &units,
		Currency: &currency,
	})

	if params.Timezone == nil || *params.Timezone != tz {
		t.Errorf("timezone = %v, want %q", params.Timezone, tz)
	}
	if params.Units == nil || *params.Units != units {
		t.Errorf("units = %v, want %q", params.Units, units)
	}
	if params.Currency == nil || *params.Currency != currency {
		t.Errorf("currency = %v, want %q", params.Currency, currency)
	}
}
