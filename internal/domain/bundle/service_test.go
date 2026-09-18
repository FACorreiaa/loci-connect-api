package bundle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo records what the service asked for. maxDays matters most: the test
// asserts the service asks the database for a truncated read, rather than
// loading everything and trimming afterwards.
type fakeRepo struct {
	bundle       *Bundle
	owned        bool
	lastMaxDays  int
	loadDaysCall int
}

func (f *fakeRepo) ListPublished(context.Context, ListFilter) ([]Bundle, int, error) {
	return []Bundle{*f.bundle}, 1, nil
}

func (f *fakeRepo) GetPublishedBySlug(_ context.Context, _ string) (*Bundle, error) {
	if f.bundle == nil {
		return nil, ErrNotFound
	}
	b := *f.bundle
	return &b, nil
}

func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*Bundle, error) {
	if f.bundle == nil {
		return nil, ErrNotFound
	}
	b := *f.bundle
	return &b, nil
}

func (f *fakeRepo) LoadDays(_ context.Context, _ uuid.UUID, maxDays int) ([]Day, error) {
	f.lastMaxDays = maxDays
	f.loadDaysCall++
	total := f.bundle.DayCount
	if maxDays > 0 && maxDays < total {
		total = maxDays
	}
	days := make([]Day, 0, total)
	for i := 1; i <= total; i++ {
		days = append(days, Day{DayNumber: i, Stops: []Stop{{Name: "stop"}}})
	}
	return days, nil
}

func (f *fakeRepo) IsOwned(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.owned, nil
}

func (f *fakeRepo) OwnedIDs(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]bool, error) {
	return map[uuid.UUID]bool{}, nil
}

func (f *fakeRepo) ListOwned(context.Context, uuid.UUID, int, int) ([]Bundle, int, error) {
	return nil, 0, nil
}
func (f *fakeRepo) RecordPurchase(context.Context, Purchase) error { return nil }
func (f *fakeRepo) MarkRefunded(context.Context, string) error     { return nil }

type fakeCheckout struct{ called bool }

func (f *fakeCheckout) CreateOneTimeCheckoutSession(context.Context, OneTimeCheckoutParams) (string, string, error) {
	f.called = true
	return "cs_test", "https://checkout.example/x", nil
}

type fakeEmails struct{}

func (fakeEmails) EmailForUser(context.Context, uuid.UUID) (string, error) {
	return "buyer@example.test", nil
}

func newSvc(t *testing.T, repo *fakeRepo, co CheckoutStarter, cfg Config) *Service {
	t.Helper()
	return NewService(repo, nil, co, fakeEmails{}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func pack(paid bool, status Status, days int) *Bundle {
	return &Bundle{ID: uuid.New(), Slug: "s", Title: "t", IsPaid: paid, Status: status, DayCount: days}
}

func TestGetDetail_Gate(t *testing.T) {
	user := uuid.New()

	cases := []struct {
		name        string
		bundle      *Bundle
		owned       bool
		userID      *uuid.UUID
		wantDays    int
		wantLocked  int
		wantMaxDays int
		wantOwned   bool
		wantErr     error
	}{
		{
			name:   "free pack, anonymous, gets everything",
			bundle: pack(false, StatusPublished, 4), userID: nil,
			wantDays: 4, wantLocked: 0, wantMaxDays: 0, wantOwned: true,
		},
		{
			name:   "paid pack, anonymous, gets day one only",
			bundle: pack(true, StatusPublished, 4), userID: nil,
			wantDays: 1, wantLocked: 3, wantMaxDays: 1, wantOwned: false,
		},
		{
			name:   "paid pack, signed in but not a buyer, gets day one only",
			bundle: pack(true, StatusPublished, 4), userID: &user, owned: false,
			wantDays: 1, wantLocked: 3, wantMaxDays: 1, wantOwned: false,
		},
		{
			name:   "paid pack, buyer, gets everything",
			bundle: pack(true, StatusPublished, 4), userID: &user, owned: true,
			wantDays: 4, wantLocked: 0, wantMaxDays: 0, wantOwned: true,
		},
		{
			name:   "retired pack, buyer, keeps what they bought",
			bundle: pack(true, StatusRetired, 3), userID: &user, owned: true,
			wantDays: 3, wantLocked: 0, wantMaxDays: 0, wantOwned: true,
		},
		{
			name:   "retired pack, not a buyer, is gone",
			bundle: pack(true, StatusRetired, 3), userID: &user, owned: false,
			wantErr: ErrNotFound,
		},
		{
			name:   "single day paid pack locks nothing it cannot show",
			bundle: pack(true, StatusPublished, 1), userID: nil,
			wantDays: 1, wantLocked: 0, wantMaxDays: 1, wantOwned: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{bundle: tc.bundle, owned: tc.owned}
			svc := newSvc(t, repo, nil, Config{})

			got, err := svc.GetDetail(context.Background(), "s", tc.userID)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.Zero(t, repo.loadDaysCall, "a pack the caller cannot see must not be read")
				return
			}
			require.NoError(t, err)
			assert.Len(t, got.Days, tc.wantDays)
			assert.Equal(t, tc.wantLocked, got.LockedDayCount)
			assert.Equal(t, tc.wantOwned, got.Owned)
			assert.Equal(t, tc.wantMaxDays, repo.lastMaxDays,
				"truncation must be asked of the database, not applied afterwards")
		})
	}
}

func TestStartCheckout_RefusesBeforeReachingStripe(t *testing.T) {
	user := uuid.New()

	cases := []struct {
		name   string
		bundle *Bundle
		owned  bool
		cfg    Config
		want   error
	}{
		{"a free pack is not for sale", pack(false, StatusPublished, 2), false, Config{PriceID: "price_x"}, ErrNotPurchasable},
		{"a draft is not for sale", pack(true, StatusDraft, 2), false, Config{PriceID: "price_x"}, ErrNotPurchasable},
		{"no configured price means no charge", pack(true, StatusPublished, 2), false, Config{}, ErrNotPurchasable},
		{"nobody pays twice", pack(true, StatusPublished, 2), true, Config{PriceID: "price_x"}, ErrAlreadyOwned},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			co := &fakeCheckout{}
			svc := newSvc(t, &fakeRepo{bundle: tc.bundle, owned: tc.owned}, co, tc.cfg)

			_, _, err := svc.StartCheckout(context.Background(), user, tc.bundle.ID, "https://a", "https://b")
			assert.True(t, errors.Is(err, tc.want), "got %v, want %v", err, tc.want)
			assert.False(t, co.called, "Stripe must not be reached when the server already knows the answer")
		})
	}
}

func TestStartCheckout_PassesKindAndBundleForTheWebhook(t *testing.T) {
	user := uuid.New()
	b := pack(true, StatusPublished, 3)
	captured := OneTimeCheckoutParams{}

	svc := newSvc(t, &fakeRepo{bundle: b}, checkoutFunc(func(p OneTimeCheckoutParams) {
		captured = p
	}), Config{PriceID: "price_configured"})

	_, _, err := svc.StartCheckout(context.Background(), user, b.ID, "https://ok", "https://no")
	require.NoError(t, err)

	assert.Equal(t, "price_configured", captured.PriceID,
		"the price comes from configuration, never from the caller")
	assert.Equal(t, PurchaseKindCityPack, captured.Metadata["loci_purchase_kind"])
	assert.Equal(t, b.ID.String(), captured.Metadata["bundle_id"])
	assert.Equal(t, user.String(), captured.Metadata["user_id"])
}

type checkoutFunc func(OneTimeCheckoutParams)

func (f checkoutFunc) CreateOneTimeCheckoutSession(_ context.Context, p OneTimeCheckoutParams) (string, string, error) {
	f(p)
	return "cs_test", "https://checkout.example/x", nil
}
