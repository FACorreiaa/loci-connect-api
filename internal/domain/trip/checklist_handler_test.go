package trip

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
)

// fakeChecklistRepo is an in-memory ChecklistRepository with the same owner and
// cap rules as the Postgres one.
type fakeChecklistRepo struct {
	owner     map[uuid.UUID]uuid.UUID // trip -> user
	items     map[uuid.UUID][]ChecklistItem
	dismissed map[uuid.UUID][]string
	err       error
}

func newFakeChecklistRepo() *fakeChecklistRepo {
	return &fakeChecklistRepo{
		owner:     map[uuid.UUID]uuid.UUID{},
		items:     map[uuid.UUID][]ChecklistItem{},
		dismissed: map[uuid.UUID][]string{},
	}
}

func (f *fakeChecklistRepo) owns(tripID, userID uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	if u, ok := f.owner[tripID]; !ok || u != userID {
		return ErrNotFound
	}
	return nil
}

func (f *fakeChecklistRepo) GetChecklist(_ context.Context, tripID, userID uuid.UUID) (*Checklist, error) {
	if err := f.owns(tripID, userID); err != nil {
		return nil, err
	}
	return &Checklist{Items: f.items[tripID], Dismissed: f.dismissed[tripID]}, nil
}

func (f *fakeChecklistRepo) UpsertItem(_ context.Context, tripID, userID uuid.UUID, item ChecklistItem) (*ChecklistItem, error) {
	if err := f.owns(tripID, userID); err != nil {
		return nil, err
	}
	item.UpdatedAt = time.Now()
	for i, it := range f.items[tripID] {
		if it.ID == item.ID {
			f.items[tripID][i] = item
			return &item, nil
		}
	}
	if len(f.items[tripID]) >= MaxChecklistItems {
		return nil, ErrChecklistFull
	}
	f.items[tripID] = append(f.items[tripID], item)
	return &item, nil
}

func (f *fakeChecklistRepo) DeleteItem(_ context.Context, tripID, userID, itemID uuid.UUID) error {
	if err := f.owns(tripID, userID); err != nil {
		return err
	}
	kept := f.items[tripID][:0]
	for _, it := range f.items[tripID] {
		if it.ID != itemID {
			kept = append(kept, it)
		}
	}
	f.items[tripID] = kept
	return nil
}

func (f *fakeChecklistRepo) DismissSuggestion(_ context.Context, tripID, userID uuid.UUID, text string) error {
	if err := f.owns(tripID, userID); err != nil {
		return err
	}
	norm := normalizeDismissal(text)
	if norm == "" {
		return errEmptyDismissal
	}
	for _, d := range f.dismissed[tripID] {
		if d == norm {
			return nil
		}
	}
	f.dismissed[tripID] = append(f.dismissed[tripID], norm)
	return nil
}

func newChecklistHandler(repo ChecklistRepository) *Handler {
	return NewHandler(&fakeTripRepo{}, "https://loci.test", nil, nil).WithChecklist(repo)
}

func packingItem(text string) *tripv1.ChecklistItem {
	return &tripv1.ChecklistItem{
		Id:   uuid.NewString(),
		Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_PACKING,
		Text: text,
	}
}

func connectCode(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return connect.CodeUnknown
}

func TestChecklist_UpsertIsIdempotentAndRoundTrips(t *testing.T) {
	repo := newFakeChecklistRepo()
	owner, tripID := uuid.New(), uuid.New()
	repo.owner[tripID] = owner
	h := newChecklistHandler(repo)
	ctx := authed(context.Background(), owner)

	expense := &tripv1.ChecklistItem{
		Id:          uuid.NewString(),
		Kind:        tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE,
		Text:        "  Museum tickets  ",
		AmountMinor: 2450,
		Currency:    "EUR",
		Position:    1,
	}
	for i := 0; i < 2; i++ { // the same upsert twice must leave one item
		resp, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{
			TripId: tripID.String(), Item: expense,
		}))
		if err != nil {
			t.Fatalf("upsert #%d: %v", i+1, err)
		}
		if resp.Msg.GetText() != "Museum tickets" {
			t.Errorf("text not trimmed: %q", resp.Msg.GetText())
		}
		if resp.Msg.GetUpdatedAt() == nil {
			t.Error("updated_at must be server-set")
		}
	}

	expense.Done = true
	if _, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{
		TripId: tripID.String(), Item: expense,
	})); err != nil {
		t.Fatalf("tick: %v", err)
	}

	got, err := h.GetTripChecklist(ctx, connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: tripID.String()}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n := len(got.Msg.GetItems()); n != 1 {
		t.Fatalf("want 1 item after replayed upserts, got %d", n)
	}
	it := got.Msg.GetItems()[0]
	if !it.GetDone() || it.GetAmountMinor() != 2450 || it.GetCurrency() != "EUR" ||
		it.GetKind() != tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE {
		t.Errorf("item did not round-trip: %+v", it)
	}

	if _, err := h.DeleteChecklistItem(ctx, connect.NewRequest(&tripv1.DeleteChecklistItemRequest{
		TripId: tripID.String(), ItemId: expense.GetId(),
	})); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Replayed delete of a missing item succeeds.
	if _, err := h.DeleteChecklistItem(ctx, connect.NewRequest(&tripv1.DeleteChecklistItemRequest{
		TripId: tripID.String(), ItemId: expense.GetId(),
	})); err != nil {
		t.Fatalf("second delete should succeed: %v", err)
	}
}

// Another user's trip must look exactly like a missing one, on every RPC.
func TestChecklist_OtherUsersTripIsNotFound(t *testing.T) {
	repo := newFakeChecklistRepo()
	owner, intruder, tripID := uuid.New(), uuid.New(), uuid.New()
	repo.owner[tripID] = owner
	h := newChecklistHandler(repo)
	ctx := authed(context.Background(), intruder)
	tid := tripID.String()

	_, err := h.GetTripChecklist(ctx, connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: tid}))
	if c := connectCode(err); c != connect.CodeNotFound {
		t.Errorf("GetTripChecklist: want NotFound, got %v (%v)", c, err)
	}
	_, err = h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{TripId: tid, Item: packingItem("Socks")}))
	if c := connectCode(err); c != connect.CodeNotFound {
		t.Errorf("UpsertChecklistItem: want NotFound, got %v (%v)", c, err)
	}
	_, err = h.DeleteChecklistItem(ctx, connect.NewRequest(&tripv1.DeleteChecklistItemRequest{TripId: tid, ItemId: uuid.NewString()}))
	if c := connectCode(err); c != connect.CodeNotFound {
		t.Errorf("DeleteChecklistItem: want NotFound, got %v (%v)", c, err)
	}
	_, err = h.DismissPackingSuggestion(ctx, connect.NewRequest(&tripv1.DismissPackingSuggestionRequest{TripId: tid, Text: "Umbrella"}))
	if c := connectCode(err); c != connect.CodeNotFound {
		t.Errorf("DismissPackingSuggestion: want NotFound, got %v (%v)", c, err)
	}
	if len(repo.items[tripID]) != 0 || len(repo.dismissed[tripID]) != 0 {
		t.Error("an intruder's writes must not land")
	}
}

func TestChecklist_CapIsResourceExhaustedButReplaysStillWork(t *testing.T) {
	repo := newFakeChecklistRepo()
	owner, tripID := uuid.New(), uuid.New()
	repo.owner[tripID] = owner
	h := newChecklistHandler(repo)
	ctx := authed(context.Background(), owner)

	var first *tripv1.ChecklistItem
	for i := 0; i < MaxChecklistItems; i++ {
		it := packingItem("item")
		if first == nil {
			first = it
		}
		if _, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{TripId: tripID.String(), Item: it})); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	_, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{TripId: tripID.String(), Item: packingItem("one too many")}))
	if c := connectCode(err); c != connect.CodeResourceExhausted {
		t.Fatalf("501st item: want ResourceExhausted, got %v (%v)", c, err)
	}
	first.Done = true
	if _, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{TripId: tripID.String(), Item: first})); err != nil {
		t.Fatalf("updating an existing item on a full list must succeed: %v", err)
	}
}

func TestChecklist_DismissalIsLowercasedTrimmedAndDeduplicated(t *testing.T) {
	repo := newFakeChecklistRepo()
	owner, tripID := uuid.New(), uuid.New()
	repo.owner[tripID] = owner
	h := newChecklistHandler(repo)
	ctx := authed(context.Background(), owner)

	for _, text := range []string{"  Compact Umbrella ", "compact umbrella"} {
		if _, err := h.DismissPackingSuggestion(ctx, connect.NewRequest(&tripv1.DismissPackingSuggestionRequest{
			TripId: tripID.String(), Text: text,
		})); err != nil {
			t.Fatalf("dismiss %q: %v", text, err)
		}
	}
	got, err := h.GetTripChecklist(ctx, connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: tripID.String()}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d := got.Msg.GetDismissedSuggestions(); len(d) != 1 || d[0] != "compact umbrella" {
		t.Errorf("want [compact umbrella], got %q", d)
	}

	_, err = h.DismissPackingSuggestion(ctx, connect.NewRequest(&tripv1.DismissPackingSuggestionRequest{TripId: tripID.String(), Text: "   "}))
	if c := connectCode(err); c != connect.CodeInvalidArgument {
		t.Errorf("blank dismissal: want InvalidArgument, got %v", c)
	}
}

func TestChecklist_RejectsBadInput(t *testing.T) {
	repo := newFakeChecklistRepo()
	owner, tripID := uuid.New(), uuid.New()
	repo.owner[tripID] = owner
	h := newChecklistHandler(repo)
	ctx := authed(context.Background(), owner)

	cases := map[string]*tripv1.ChecklistItem{
		"nil item":       nil,
		"non-uuid id":    {Id: "abc", Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_PACKING, Text: "x"},
		"no kind":        {Id: uuid.NewString(), Text: "x"},
		"blank text":     {Id: uuid.NewString(), Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_PACKING, Text: "  "},
		"negative":       {Id: uuid.NewString(), Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE, Text: "x", AmountMinor: -1},
		"lower currency": {Id: uuid.NewString(), Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE, Text: "x", Currency: "eur"},
		"long currency":  {Id: uuid.NewString(), Kind: tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE, Text: "x", Currency: "EURO"},
	}
	for name, item := range cases {
		_, err := h.UpsertChecklistItem(ctx, connect.NewRequest(&tripv1.UpsertChecklistItemRequest{TripId: tripID.String(), Item: item}))
		if c := connectCode(err); c != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v (%v)", name, c, err)
		}
	}

	_, err := h.GetTripChecklist(ctx, connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: "not-a-uuid"}))
	if c := connectCode(err); c != connect.CodeInvalidArgument {
		t.Errorf("bad trip id: want InvalidArgument, got %v", c)
	}
}

func TestChecklist_RequiresAuthAndConfiguration(t *testing.T) {
	h := newChecklistHandler(newFakeChecklistRepo())
	_, err := h.GetTripChecklist(context.Background(), connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: uuid.NewString()}))
	if c := connectCode(err); c != connect.CodeUnauthenticated {
		t.Errorf("no user: want Unauthenticated, got %v", c)
	}

	bare := NewHandler(&fakeTripRepo{}, "https://loci.test", nil, nil)
	_, err = bare.GetTripChecklist(authed(context.Background(), uuid.New()), connect.NewRequest(&tripv1.GetTripChecklistRequest{TripId: uuid.NewString()}))
	if c := connectCode(err); c != connect.CodeUnimplemented {
		t.Errorf("no store: want Unimplemented, got %v", c)
	}
}
