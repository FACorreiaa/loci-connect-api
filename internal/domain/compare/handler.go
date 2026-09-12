package compare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1/comparev1connect"
	"github.com/google/uuid"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const (
	// suggestionsHeader carries "did you mean" cities on an error, following the
	// x-loci-entitlement pattern the client already knows how to read.
	suggestionsHeader = "x-loci-city-suggestions"
	// Metadata travels on every response; a long list does not belong there.
	maxSuggestionsOnWire = 5
	maxSuggestionBytes   = 1024
)

// Handler implements CompareService.
type Handler struct {
	comparev1connect.UnimplementedCompareServiceHandler
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CompareWeekend(
	ctx context.Context,
	req *connect.Request[comparev1.CompareWeekendRequest],
) (*connect.Response[comparev1.CompareWeekendResponse], error) {
	if h.svc == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("compare service unavailable"))
	}

	candidates := req.Msg.GetCandidateCityNames()
	if len(candidates) < 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("provide at least 2 candidate cities"))
	}

	var uid uuid.UUID
	if s, ok := interceptors.GetUserIDFromContext(ctx); ok && s != "" {
		if parsed, err := uuid.Parse(s); err == nil {
			uid = parsed
		}
	}

	ent := EntitlementsForUser(ctx, h.svc.plans, uid)
	if len(candidates) > ent.MaxCandidates {
		candidates = candidates[:ent.MaxCandidates]
	}

	start := req.Msg.GetStartDate().AsTime()
	end := req.Msg.GetEndDate().AsTime()

	out, err := h.svc.CompareWeekend(ctx, CompareInput{
		OriginCity:       req.Msg.GetOriginCity(),
		OriginLat:        req.Msg.GetOriginLat(),
		OriginLon:        req.Msg.GetOriginLon(),
		Candidates:       candidates,
		Start:            start,
		End:              end,
		UserID:           uid,
		Allow3Candidates: ent.MaxCandidates > FreeMaxCandidates,
		AllowDualCity:    ent.AllowMultiCity,
	})
	if err != nil {
		return nil, compareError(err)
	}
	return connect.NewResponse(out), nil
}

// compareError gives a failure the status code it deserves.
//
// Every failure here used to be InvalidArgument, which is how a request for
// Porto — a real city, correctly spelled — came back as a 400 telling the user
// to fix their input. Worse, it meant a geocoder outage or a database failure
// also reported as the user's fault, so an entire class of our own breakage was
// invisible to anything counting 5xx.
//
// NotFound is deliberately not used: the subject of this RPC is the comparison,
// not a city, and a NotFound would read as "no such page".
func compareError(err error) error {
	// An unresolvable name is the user's to fix, and the near-misses are worth
	// handing back so the UI can offer them rather than just saying no.
	var ambiguous *cityrepo.AmbiguousCityError
	if errors.As(err, &ambiguous) && len(ambiguous.Suggestions) > 0 {
		ce := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("compare: %w", err))
		attachSuggestions(ce, ambiguous.Suggestions)
		return ce
	}

	switch {
	case errors.Is(err, cityrepo.ErrGeocoderUnavailable):
		// Retrying is the correct client action. Telling it to change its input
		// instead would be a lie.
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("compare: %w", err))
	case errors.Is(err, cityrepo.ErrCityUnresolvable), errors.Is(err, ErrTooFewResolvable):
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("compare: %w", err))
	}

	// Cancellation, deadlines and anything genuinely internal. apierr is the
	// established mapping; a second one here would drift from it.
	return apierr.ToConnect(fmt.Errorf("compare: %w", err))
}

// attachSuggestions puts "did you mean" candidates in the error metadata.
//
// Metadata rather than a response field because this is an error path — a typed
// response carrying an error would mean answering 200 with a failure. Base64
// because Connect metadata values are ASCII and city names such as "Évora" are
// not.
func attachSuggestions(ce *connect.Error, suggestions []cityrepo.Suggestion) {
	if len(suggestions) > maxSuggestionsOnWire {
		suggestions = suggestions[:maxSuggestionsOnWire]
	}
	payload, err := json.Marshal(suggestions)
	if err != nil || len(payload) > maxSuggestionBytes {
		return
	}
	ce.Meta().Set(suggestionsHeader, base64.RawURLEncoding.EncodeToString(payload))
}
