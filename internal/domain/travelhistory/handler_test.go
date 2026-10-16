package travelhistory

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeVisitRepo struct {
	Repository
	visitID *uuid.UUID
}

func (f *fakeVisitRepo) RecordVisit(_ context.Context, _ uuid.UUID, in VisitInput) (*VisitedCity, *uuid.UUID, error) {
	if in.POIID == "" {
		return &VisitedCity{CityName: in.CityName, VisitCount: 1}, nil, nil
	}
	return &VisitedCity{CityName: in.CityName, VisitCount: 1}, f.visitID, nil
}

type recordedSignal struct {
	userID uuid.UUID
	event  string
	opts   preference.RecordOpts
}

type fakeTaste struct {
	signals []recordedSignal
	kicks   []uuid.UUID
}

func (f *fakeTaste) Record(_ context.Context, userID uuid.UUID, event string, opts preference.RecordOpts) {
	f.signals = append(f.signals, recordedSignal{userID: userID, event: event, opts: opts})
}

func (f *fakeTaste) Replace(context.Context, uuid.UUID, string, string, string, preference.RecordOpts) {
}

func (f *fakeTaste) Forget(context.Context, uuid.UUID, string, string) {}

func (f *fakeTaste) Kick(userID uuid.UUID) { f.kicks = append(f.kicks, userID) }

func authed(userID uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, userID.String())
}

func TestRecordVisit_WithPOI_RecordsVisitedSignal(t *testing.T) {
	visitID := uuid.New()
	tripID := uuid.New()
	taste := &fakeTaste{}
	h := NewHandler(&fakeVisitRepo{visitID: &visitID}, nil).WithTaste(taste, taste)
	user := uuid.New()
	poiID := "poi-123"

	_, err := h.RecordVisit(authed(user), connect.NewRequest(&travelhistoryv1.RecordVisitRequest{
		CityName: "Lisbon", Latitude: 38.71, Longitude: -9.14,
		TripId: tripID.String(), PoiId: &poiID,
	}))
	if err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	if len(taste.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(taste.signals))
	}
	got := taste.signals[0]
	if got.userID != user || got.event != preference.EventVisited || got.opts.POIID != poiID {
		t.Fatalf("signal = %+v", got)
	}
	if got.opts.TripID == nil || *got.opts.TripID != tripID {
		t.Fatalf("trip id = %v, want %v", got.opts.TripID, tripID)
	}
	if got.opts.Metadata["visit_id"] != visitID.String() {
		t.Fatalf("visit_id metadata = %v, want %s", got.opts.Metadata["visit_id"], visitID)
	}
	if len(taste.kicks) != 1 || taste.kicks[0] != user {
		t.Fatalf("kicks = %v, want [%s]", taste.kicks, user)
	}
}

func TestRecordVisit_WithoutPOI_NoSignal(t *testing.T) {
	taste := &fakeTaste{}
	h := NewHandler(&fakeVisitRepo{}, nil).WithTaste(taste, taste)
	_, err := h.RecordVisit(authed(uuid.New()), connect.NewRequest(&travelhistoryv1.RecordVisitRequest{
		CityName: "Lisbon", Latitude: 38.71, Longitude: -9.14,
	}))
	if err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	if len(taste.signals) != 0 || len(taste.kicks) != 0 {
		t.Fatalf("signals %d kicks %d, want none", len(taste.signals), len(taste.kicks))
	}
}

// A replayed visit (same POI, same instant) inserts nothing, so it must not
// teach taste twice either.
func TestRecordVisit_ReplayedVisit_NoSecondSignal(t *testing.T) {
	taste := &fakeTaste{}
	h := NewHandler(&fakeVisitRepo{visitID: nil}, nil).WithTaste(taste, taste)
	poiID := "poi-123"
	_, err := h.RecordVisit(authed(uuid.New()), connect.NewRequest(&travelhistoryv1.RecordVisitRequest{
		CityName: "Lisbon", Latitude: 38.71, Longitude: -9.14, PoiId: &poiID,
	}))
	if err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	if len(taste.signals) != 0 {
		t.Fatalf("signals = %d, want 0", len(taste.signals))
	}
}

func (f *fakeVisitRepo) DeleteVisitedPOI(_ context.Context, _, id uuid.UUID) (bool, error) {
	return f.visitID != nil && *f.visitID == id, nil
}

type forgettingTaste struct {
	fakeTaste
	forgot []string
}

func (f *forgettingTaste) Forget(_ context.Context, _ uuid.UUID, key, value string) {
	f.forgot = append(f.forgot, key+"="+value)
}

// Deleting a place visit also removes the taste signal it produced, keyed by
// the visit id learnFromVisit stored.
func TestDeleteVisitedPOI_ForgetsItsSignal(t *testing.T) {
	visitID := uuid.New()
	taste := &forgettingTaste{}
	h := NewHandler(&fakeVisitRepo{visitID: &visitID}, nil).WithTaste(taste, taste)
	user := uuid.New()

	resp, err := h.DeleteVisitedPOI(authed(user), connect.NewRequest(&travelhistoryv1.DeleteVisitedPOIRequest{Id: visitID.String()}))
	if err != nil {
		t.Fatalf("DeleteVisitedPOI: %v", err)
	}
	if !resp.Msg.GetDeleted() {
		t.Fatal("deleted = false, want true")
	}
	if len(taste.forgot) != 1 || taste.forgot[0] != "visit_id="+visitID.String() {
		t.Fatalf("forgot = %v", taste.forgot)
	}
	if len(taste.kicks) != 1 {
		t.Fatalf("kicks = %d, want 1", len(taste.kicks))
	}
}

func TestDeleteVisitedPOI_UnknownIDForgetsNothing(t *testing.T) {
	taste := &forgettingTaste{}
	h := NewHandler(&fakeVisitRepo{}, nil).WithTaste(taste, taste)
	resp, err := h.DeleteVisitedPOI(authed(uuid.New()), connect.NewRequest(&travelhistoryv1.DeleteVisitedPOIRequest{Id: uuid.NewString()}))
	if err != nil {
		t.Fatalf("DeleteVisitedPOI: %v", err)
	}
	if resp.Msg.GetDeleted() || len(taste.forgot) != 0 || len(taste.kicks) != 0 {
		t.Fatalf("deleted=%v forgot=%v kicks=%d, want nothing", resp.Msg.GetDeleted(), taste.forgot, len(taste.kicks))
	}
}

func TestVisitInputFromProto_Source(t *testing.T) {
	base := func() *travelhistoryv1.RecordVisitRequest {
		return &travelhistoryv1.RecordVisitRequest{CityName: "Lisbon", Latitude: 38.7, Longitude: -9.1}
	}
	unset, err := visitInputFromProto(base())
	if err != nil || unset.Source != SourceManual {
		t.Fatalf("unset source = %q, %v; want manual", unset.Source, err)
	}
	walked := base()
	walked.Source = travelhistoryv1.VisitSource_VISIT_SOURCE_VISIT_EVENT.Enum()
	got, err := visitInputFromProto(walked)
	if err != nil || got.Source != SourceVisitEvent {
		t.Fatalf("visit-event source = %q, %v; want visit_event", got.Source, err)
	}
}
