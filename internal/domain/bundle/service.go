package bundle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// Errors the handler maps onto Connect codes.
var (
	// ErrAlreadyOwned is returned when a caller tries to buy a pack twice.
	ErrAlreadyOwned = errors.New("bundle already owned")
	// ErrNotPurchasable is returned for a free pack, or one with no price
	// configured. Neither is a customer error worth a payment page.
	ErrNotPurchasable = errors.New("bundle is not for sale")
	// ErrNotOwned is returned when a caller asks for something only a buyer
	// may have.
	ErrNotOwned = errors.New("bundle not owned")
)

// freePreviewDays is how much of a paid pack anybody can read.
//
// One day, complete with its stops and coordinates, so the map draws and the
// quality of the writing is visible before money is asked for. It mirrors the
// split users already meet in the Trip Kit, where free gets day one.
const freePreviewDays = 1

// CheckoutStarter is the slice of the payment service this domain needs. It is
// declared here, rather than imported as a concrete type, so that payment can
// depend on bundle for fulfilment without the two packages importing each
// other.
type CheckoutStarter interface {
	CreateOneTimeCheckoutSession(ctx context.Context, p OneTimeCheckoutParams) (sessionID, url string, err error)
}

// OneTimeCheckoutParams is a single non-recurring Stripe Checkout session.
type OneTimeCheckoutParams struct {
	UserID     uuid.UUID
	Email      string
	PriceID    string
	SuccessURL string
	CancelURL  string
	Metadata   map[string]string
}

// TripWriter writes the trip a claimed pack becomes.
type TripWriter interface {
	SaveTrip(ctx context.Context, t *trip.Trip, baseVersion int64) (*trip.Trip, error)
}

// EmailLookup resolves the caller's email for Stripe.
type EmailLookup interface {
	EmailForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

// Config is what the service needs to know about money.
type Config struct {
	// PriceID is the single Stripe price every pack is sold at. Empty disables
	// selling entirely, which is the correct state before Stripe is set up:
	// the catalog still serves, and checkout refuses rather than charging an
	// amount nobody configured.
	PriceID    string
	PriceCents int
	Currency   string
	PublicBase string
}

// Detail is one pack with as much of it as the caller may see.
type Detail struct {
	Bundle         *Bundle
	Days           []Day
	LockedDayCount int
	Owned          bool
}

// Service is the read and purchase side of City Packs.
type Service struct {
	repo     Repository
	trips    TripWriter
	checkout CheckoutStarter
	emails   EmailLookup
	cfg      Config
	logger   *slog.Logger
}

// NewService builds the bundle service. checkout, trips and emails may be nil
// in a deployment that only serves the catalog.
func NewService(repo Repository, trips TripWriter, checkout CheckoutStarter, emails EmailLookup, cfg Config, logger *slog.Logger) *Service {
	return &Service{repo: repo, trips: trips, checkout: checkout, emails: emails, cfg: cfg, logger: logger}
}

// PriceCents is the amount a pack costs, for display.
func (s *Service) PriceCents() int { return s.cfg.PriceCents }

// Currency is the currency packs are sold in.
func (s *Service) Currency() string {
	if s.cfg.Currency == "" {
		return "usd"
	}
	return s.cfg.Currency
}

// List returns the published catalog. userID may be nil for an anonymous
// caller; when it is set, each pack reports whether this person owns it.
func (s *Service) List(ctx context.Context, f ListFilter, userID *uuid.UUID) ([]Bundle, map[uuid.UUID]bool, int, error) {
	bundles, total, err := s.repo.ListPublished(ctx, f)
	if err != nil {
		return nil, nil, 0, err
	}

	owned := map[uuid.UUID]bool{}
	if userID != nil && len(bundles) > 0 {
		ids := make([]uuid.UUID, 0, len(bundles))
		for _, b := range bundles {
			if b.IsPaid {
				ids = append(ids, b.ID)
			}
		}
		owned, err = s.repo.OwnedIDs(ctx, *userID, ids)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	return bundles, owned, total, nil
}

// GetDetail returns a pack with the days the caller is entitled to.
//
// The truncation happens here and in the repository, never in the client: for
// a paid pack the caller does not own, the later days are not read out of the
// database at all.
func (s *Service) GetDetail(ctx context.Context, slug string, userID *uuid.UUID) (*Detail, error) {
	b, err := s.repo.GetPublishedBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}

	owned := !b.IsPaid
	if b.IsPaid && userID != nil {
		owned, err = s.repo.IsOwned(ctx, *userID, b.ID)
		if err != nil {
			return nil, err
		}
	}

	// A retired pack is off sale. Its owners keep it; nobody else can reach it,
	// not even as a preview, because there is no way to buy the rest.
	if b.Status == StatusRetired && !owned {
		return nil, ErrNotFound
	}

	maxDays := 0
	if !owned {
		maxDays = freePreviewDays
	}

	days, err := s.repo.LoadDays(ctx, b.ID, maxDays)
	if err != nil {
		return nil, err
	}

	locked := 0
	if !owned && b.DayCount > len(days) {
		locked = b.DayCount - len(days)
	}

	b.Days = days
	return &Detail{Bundle: b, Days: days, LockedDayCount: locked, Owned: owned}, nil
}

// StartCheckout opens a one-time Stripe Checkout session for a pack.
//
// The price is read from configuration against a pack the server looked up
// itself. Nothing about the amount comes from the caller, which is why this
// is a separate entry point rather than the subscription checkout with a mode
// flag: that one deliberately refuses to honour a client-supplied mode.
func (s *Service) StartCheckout(ctx context.Context, userID, bundleID uuid.UUID, successURL, cancelURL string) (sessionID, url string, err error) {
	if s.checkout == nil || s.emails == nil {
		return "", "", ErrNotPurchasable
	}

	b, err := s.repo.GetByID(ctx, bundleID)
	if err != nil {
		return "", "", err
	}
	if b.Status != StatusPublished || !b.IsPaid {
		return "", "", ErrNotPurchasable
	}
	if s.cfg.PriceID == "" {
		s.logger.Error("bundle checkout attempted with no price configured", "bundle_id", bundleID)
		return "", "", ErrNotPurchasable
	}

	owned, err := s.repo.IsOwned(ctx, userID, bundleID)
	if err != nil {
		return "", "", err
	}
	if owned {
		return "", "", ErrAlreadyOwned
	}

	email, err := s.emails.EmailForUser(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("resolve buyer email: %w", err)
	}

	return s.checkout.CreateOneTimeCheckoutSession(ctx, OneTimeCheckoutParams{
		UserID:     userID,
		Email:      email,
		PriceID:    s.cfg.PriceID,
		SuccessURL: successURL,
		CancelURL:  cancelURL,
		// The webhook reads these back. kind is what tells a pack purchase
		// apart from a subscription in the same checkout.session.completed
		// branch.
		Metadata: map[string]string{
			"loci_purchase_kind": PurchaseKindCityPack,
			"bundle_id":          bundleID.String(),
			"user_id":            userID.String(),
		},
	})
}

// PurchaseKindCityPack marks a Stripe session as a pack purchase.
const PurchaseKindCityPack = "city_pack"

// Fulfil grants ownership after Stripe confirms payment. It is called from the
// webhook and is safe to call more than once for the same session.
func (s *Service) Fulfil(ctx context.Context, p Purchase) error {
	if err := s.repo.RecordPurchase(ctx, p); err != nil {
		return err
	}
	s.logger.Info("city pack purchase fulfilled",
		"user_id", p.UserID, "bundle_id", p.BundleID, "session", p.StripeCheckoutSessionID)
	return nil
}

// Refund revokes ownership for a refunded payment intent.
func (s *Service) Refund(ctx context.Context, paymentIntentID string) error {
	return s.repo.MarkRefunded(ctx, paymentIntentID)
}

// ListMine returns the packs a caller has bought.
func (s *Service) ListMine(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Bundle, int, error) {
	return s.repo.ListOwned(ctx, userID, page, pageSize)
}

// Claim writes a pack into the caller's own trips.
//
// The result is an ordinary trip: editable, exportable, shareable, and
// untouched by later edits to the pack it came from. A pack is a template, so
// claiming one twice is allowed and produces two trips.
func (s *Service) Claim(ctx context.Context, userID, bundleID uuid.UUID) (uuid.UUID, error) {
	if s.trips == nil {
		return uuid.Nil, ErrNotPurchasable
	}

	b, err := s.repo.GetByID(ctx, bundleID)
	if err != nil {
		return uuid.Nil, err
	}
	if b.Status != StatusPublished && b.Status != StatusRetired {
		return uuid.Nil, ErrNotFound
	}

	if b.IsPaid {
		owned, err := s.repo.IsOwned(ctx, userID, bundleID)
		if err != nil {
			return uuid.Nil, err
		}
		if !owned {
			return uuid.Nil, ErrNotOwned
		}
	}

	days, err := s.repo.LoadDays(ctx, b.ID, 0)
	if err != nil {
		return uuid.Nil, err
	}

	t := &trip.Trip{
		// Nil id means "new trip" to SaveTrip.
		UserID:   userID,
		CityID:   b.CityID,
		CityName: b.CityName,
		Title:    b.Title,
		Days:     make([]trip.TripDay, 0, len(days)),
	}
	for _, d := range days {
		td := trip.TripDay{
			DayNumber: int32(d.DayNumber),
			CityID:    b.CityID,
			CityName:  b.CityName,
			Stops:     make([]trip.TripStop, 0, len(d.Stops)),
		}
		for _, st := range d.Stops {
			ts := trip.TripStop{
				OrderIndex: int32(st.OrderIndex),
				Name:       st.Name,
				Notes:      st.Notes,
				BookingURL: st.BookingURL,
			}
			// A pack stop may have lost its POI link to a merge. The trip keeps
			// the name either way; trip_stops.poi_id is a plain string there.
			if st.POIID != nil {
				ts.POIID = st.POIID.String()
			}
			if st.StartMinute != nil {
				v := int32(*st.StartMinute)
				ts.StartMinute = &v
			}
			if st.DurationMinutes != nil {
				v := int32(*st.DurationMinutes)
				ts.DurationMinutes = &v
			}
			td.Stops = append(td.Stops, ts)
		}
		t.Days = append(t.Days, td)
	}

	saved, err := s.trips.SaveTrip(ctx, t, 0)
	if err != nil {
		return uuid.Nil, fmt.Errorf("save claimed trip: %w", err)
	}
	return saved.ID, nil
}
