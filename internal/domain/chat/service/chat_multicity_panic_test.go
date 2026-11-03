package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Each city runs on its own goroutine, outside the handler's recover. A panic
// in one used to take down the process — every stream on the pod — instead of
// failing that one city.
func TestProcessMultiCity_PanickingCityFailsOnlyThatCity(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 200)
	route := twoCityRoute()
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		if cc.CityName == "Porto" {
			panic("porto generation blew up")
		}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: cc.PresetSessionID.String()}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, Data: locitypes.AiCityResponse{}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeComplete}
		return &locitypes.AiCityResponse{}, nil
	}

	err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), UserID: uuid.New(), EventCh: events}, route)
	close(events)
	if err != nil {
		t.Fatalf("one city panicking must not fail the run: %v", err)
	}

	portoFailed := false
	for ev := range events {
		if ev.Type == locitypes.EventTypeError && ev.StopIndex != nil && *ev.StopIndex == 1 {
			portoFailed = true
		}
	}
	if !portoFailed {
		t.Fatal("the panicking city must be reported as a failed stop")
	}
}
