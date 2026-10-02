package messaging

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Trip action buttons. A press carries the proposal's id (and, for a pick,
// the option), never the change itself: the proposal lives on the server.
// "a|<32 hex>|<option or ->" is at most 36 bytes, well under Telegram's 64.
const (
	applyTokenPrefix   = "a"
	dismissTokenPrefix = "d"
	tripTokenSep       = "|"
	noOption           = "-"
)

func hex32(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }

// ApplyToken is the data of a button that applies a trip proposal.
func ApplyToken(proposalID uuid.UUID, option *int) string {
	opt := noOption
	if option != nil {
		opt = strconv.Itoa(*option)
	}
	return applyTokenPrefix + tripTokenSep + hex32(proposalID) + tripTokenSep + opt
}

// DismissToken is the data of a button that drops a trip proposal.
func DismissToken(proposalID uuid.UUID) string {
	return dismissTokenPrefix + tripTokenSep + hex32(proposalID)
}

// IsApplyToken reports whether a press applies a trip proposal: the one press
// that can run a model generation (a re-plan), so it gets longer than a page.
func IsApplyToken(data string) bool {
	return strings.HasPrefix(data, applyTokenPrefix+tripTokenSep)
}

type tripToken struct {
	apply      bool
	proposalID uuid.UUID
	option     *int
}

func parseTripToken(data string) (tripToken, bool) {
	parts := strings.Split(data, tripTokenSep)
	switch {
	case len(parts) == 3 && parts[0] == applyTokenPrefix:
	case len(parts) == 2 && parts[0] == dismissTokenPrefix:
	default:
		return tripToken{}, false
	}
	if len(parts[1]) != 32 {
		return tripToken{}, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return tripToken{}, false
	}
	tok := tripToken{apply: parts[0] == applyTokenPrefix, proposalID: id}
	if tok.apply && parts[2] != noOption {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 {
			return tripToken{}, false
		}
		tok.option = &n
	}
	return tok, true
}

// TripCard is one proposed trip change: a message and its buttons.
type TripCard struct {
	Text    string
	Buttons []Button
}

// TripPlanner proposes and applies changes to the trip a chat is about.
type TripPlanner interface {
	// Propose returns cards when text asks to change the trip the user's
	// latest conversation produced, and none (with a nil error) otherwise.
	Propose(ctx context.Context, userID uuid.UUID, email, text string) ([]TripCard, error)
	// Apply makes the change and returns what to tell the traveller: the
	// confirmation, or in plain words why it could not be made.
	Apply(ctx context.Context, userID uuid.UUID, email string, proposalID uuid.UUID, option *int) string
	// Dismiss drops the proposal and returns the reply.
	Dismiss(ctx context.Context, userID, proposalID uuid.UUID) string
}

// WithTripPlanner lets messages about a trip propose changes to it, applied
// with a button. Without it every message is answered as before.
func (s *Service) WithTripPlanner(p TripPlanner) *Service {
	s.trips = p
	return s
}

// tripsLead heads a turn that proposes changes; each proposal follows as its
// own message, so a press clears only its own buttons.
const tripsLead = "Here's what I can change on your trip. Tap to confirm:"
