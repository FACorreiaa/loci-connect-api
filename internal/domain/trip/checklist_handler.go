package trip

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WithChecklist attaches the checklist store. Without it the checklist RPCs
// answer Unimplemented rather than panicking.
func (h *Handler) WithChecklist(repo ChecklistRepository) *Handler {
	h.checklist = repo
	return h
}

// checklistCall resolves what every checklist RPC needs: the caller, the trip
// id, and a configured store.
func (h *Handler) checklistCall(ctx context.Context, rawTripID string) (uuid.UUID, uuid.UUID, error) {
	if h.checklist == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errors.New("trip checklists are not configured"))
	}
	uid, err := userID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	tripID, err := uuid.Parse(rawTripID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	return uid, tripID, nil
}

func checklistErr(err error) error {
	switch {
	case errors.Is(err, ErrChecklistFull):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, errEmptyDismissal):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return toConnectErr(err)
	}
}

// GetTripChecklist returns the trip's packing items, expenses and dismissed
// suggestions. Another user's trip is NotFound, like GetTrip.
func (h *Handler) GetTripChecklist(
	ctx context.Context,
	req *connect.Request[tripv1.GetTripChecklistRequest],
) (*connect.Response[tripv1.GetTripChecklistResponse], error) {
	uid, tripID, err := h.checklistCall(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	cl, err := h.checklist.GetChecklist(ctx, tripID, uid)
	if err != nil {
		return nil, checklistErr(err)
	}
	out := &tripv1.GetTripChecklistResponse{
		Items:                make([]*tripv1.ChecklistItem, 0, len(cl.Items)),
		DismissedSuggestions: cl.Dismissed,
	}
	for i := range cl.Items {
		out.Items = append(out.Items, checklistItemToProto(&cl.Items[i]))
	}
	return connect.NewResponse(out), nil
}

// UpsertChecklistItem creates or replaces an item by (trip_id, item.id). The id
// is the client's, so replaying the same upsert is harmless.
func (h *Handler) UpsertChecklistItem(
	ctx context.Context,
	req *connect.Request[tripv1.UpsertChecklistItemRequest],
) (*connect.Response[tripv1.ChecklistItem], error) {
	uid, tripID, err := h.checklistCall(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	item, err := checklistItemFromProto(req.Msg.GetItem())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	saved, err := h.checklist.UpsertItem(ctx, tripID, uid, item)
	if err != nil {
		return nil, checklistErr(err)
	}
	return connect.NewResponse(checklistItemToProto(saved)), nil
}

// DeleteChecklistItem removes an item. A missing item is not an error.
func (h *Handler) DeleteChecklistItem(
	ctx context.Context,
	req *connect.Request[tripv1.DeleteChecklistItemRequest],
) (*connect.Response[tripv1.DeleteChecklistItemResponse], error) {
	uid, tripID, err := h.checklistCall(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	itemID, err := uuid.Parse(req.Msg.GetItemId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid item ID"))
	}
	if err := h.checklist.DeleteItem(ctx, tripID, uid, itemID); err != nil {
		return nil, checklistErr(err)
	}
	return connect.NewResponse(&tripv1.DeleteChecklistItemResponse{}), nil
}

// DismissPackingSuggestion hides a suggestion for this trip. Stored lowercased
// and trimmed; dismissing the same text twice is a no-op.
func (h *Handler) DismissPackingSuggestion(
	ctx context.Context,
	req *connect.Request[tripv1.DismissPackingSuggestionRequest],
) (*connect.Response[tripv1.DismissPackingSuggestionResponse], error) {
	uid, tripID, err := h.checklistCall(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	if err := h.checklist.DismissSuggestion(ctx, tripID, uid, req.Msg.GetText()); err != nil {
		return nil, checklistErr(err)
	}
	return connect.NewResponse(&tripv1.DismissPackingSuggestionResponse{}), nil
}

// checklistItemFromProto validates what protovalidate already checks at the
// edge, so the handler is safe when called directly (tests, other transports).
func checklistItemFromProto(p *tripv1.ChecklistItem) (ChecklistItem, error) {
	if p == nil {
		return ChecklistItem{}, errors.New("item is required")
	}
	id, err := uuid.Parse(p.GetId())
	if err != nil {
		return ChecklistItem{}, errors.New("invalid item ID")
	}
	var kind ChecklistKind
	switch p.GetKind() {
	case tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_PACKING:
		kind = ChecklistKindPacking
	case tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE:
		kind = ChecklistKindExpense
	default:
		return ChecklistItem{}, errors.New("item kind is required")
	}
	text := strings.TrimSpace(p.GetText())
	if text == "" || len([]rune(text)) > 300 {
		return ChecklistItem{}, errors.New("item text must be 1 to 300 characters")
	}
	if p.GetAmountMinor() < 0 {
		return ChecklistItem{}, errors.New("amount must not be negative")
	}
	if !validCurrency(p.GetCurrency()) {
		return ChecklistItem{}, errors.New("currency must be an ISO 4217 code or empty")
	}
	if p.GetPosition() < 0 {
		return ChecklistItem{}, errors.New("position must not be negative")
	}
	return ChecklistItem{
		ID:          id,
		Kind:        kind,
		Text:        text,
		Done:        p.GetDone(),
		AmountMinor: p.GetAmountMinor(),
		Currency:    p.GetCurrency(),
		Position:    p.GetPosition(),
	}, nil
}

// validCurrency accepts "" or three uppercase ASCII letters.
func validCurrency(c string) bool {
	if c == "" {
		return true
	}
	if len(c) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
}

func checklistItemToProto(it *ChecklistItem) *tripv1.ChecklistItem {
	kind := tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_UNSPECIFIED
	switch it.Kind {
	case ChecklistKindPacking:
		kind = tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_PACKING
	case ChecklistKindExpense:
		kind = tripv1.ChecklistItemKind_CHECKLIST_ITEM_KIND_EXPENSE
	}
	out := &tripv1.ChecklistItem{
		Id:          it.ID.String(),
		Kind:        kind,
		Text:        it.Text,
		Done:        it.Done,
		AmountMinor: it.AmountMinor,
		Currency:    it.Currency,
		Position:    it.Position,
	}
	if !it.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(it.UpdatedAt)
	}
	return out
}
