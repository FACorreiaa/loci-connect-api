package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// TripActionProposer turns a message about a trip into proposed changes.
type TripActionProposer interface {
	Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]tripaction.Proposal, error)
}

// SetTripActions lets turns bound to a trip propose changes to it instead of
// regenerating. Nil (the default) answers every turn as before.
func (l *ServiceImpl) SetTripActions(p TripActionProposer) { l.tripActions = p }

// ActionLLM is the extraction call trip actions make: this service's model
// chain, behind the same LLM slots every generation takes.
func (l *ServiceImpl) ActionLLM() tripaction.Generator { return actionLLM{l} }

type actionLLM struct{ l *ServiceImpl }

func (a actionLLM) GenerateText(ctx context.Context, prompt string) (string, error) {
	release, err := a.l.acquireLLMSlot(ctx)
	if err != nil {
		return "", fmt.Errorf("LLM capacity exceeded: %w", err)
	}
	defer release()
	return a.l.aiClient.GenerateText(ctx, prompt, &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](0.1)})
}

// proposeTripActions handles a trip-bound turn that asks for changes: one
// action_proposal event per change, then completion. false means the
// message asked for none, or extraction failed, and the turn is answered as
// usual; extraction trouble never costs the traveller their answer.
func (l *ServiceImpl) proposeTripActions(cc common.ChatContext) bool {
	props, err := l.tripActions.Propose(cc.Ctx, cc.UserID, cc.TripID, cc.RequestedSessionID, cc.Message)
	if err != nil {
		l.logger.WarnContext(cc.Ctx, "trip actions: proposing failed; answering instead", slog.Any("error", err))
		return false
	}
	if len(props) == 0 {
		return false
	}
	for _, p := range props {
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeActionProposal, Message: p.Summary, Data: p,
		}, 3)
	}
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete, Data: "Turn completed.", IsFinal: true,
	}, 3)
	return true
}

// GenerateDays plans days for city without saving a trip, for a confirmed
// "re-plan as N days". It runs the same per-city generation a multi-city stop
// does (runStop): city preset, trip length preset, no trip save.
func (l *ServiceImpl) GenerateDays(ctx context.Context, userID uuid.UUID, cityName string, days int) ([]trip.TripDay, error) {
	ch := make(chan locitypes.StreamEvent, 100)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch { //nolint:revive // nobody watches a re-plan's stream
		}
	}()
	cc := common.ChatContext{
		Ctx: ctx, UserID: userID, CityName: cityName,
		Message: fmt.Sprintf("Plan a %d-day itinerary in %s", days, cityName),
		EventCh: ch, StopRun: true, PresetTripDays: days, SuppressTripSave: true,
	}
	data, err := l.runCity(cc)
	close(ch)
	<-drained
	if err != nil {
		return nil, err
	}
	cc.TripDays = days
	tr := buildTripFromCityResponse(&cc, data, "")
	if tr == nil || len(tr.Days) == 0 {
		return nil, errors.New("the re-plan produced no days")
	}
	return tr.Days, nil
}
