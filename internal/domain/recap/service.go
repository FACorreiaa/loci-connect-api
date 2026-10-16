package recap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReasonNotEnded is shown when someone opens the recap of a trip still under
// way.
const ReasonNotEnded = "Your recap is ready the day after your trip ends."

// ReasonYearLocked is shown on the year in review before it unlocks.
const ReasonYearLocked = "Your year in review unlocks on 1 December."

// Notifier announces a ready recap (push.Notifier via an adapter).
type Notifier interface {
	// RecapReady tells to that the recap of tripID is ready.
	RecapReady(ctx context.Context, to, tripID uuid.UUID, title, body string)
}

type noopNotifier struct{}

func (noopNotifier) RecapReady(context.Context, uuid.UUID, uuid.UUID, string, string) {}

// Service runs recaps: building on request, the runner's queue, sharing.
type Service struct {
	repo     Repository
	builder  *Builder
	notifier Notifier
	origin   string
	log      *slog.Logger
	now      func() time.Time
}

// NewService builds the service. origin is the web app's origin, which share
// links point at.
func NewService(repo Repository, origin string, notifier Notifier, log *slog.Logger) *Service {
	if notifier == nil {
		notifier = noopNotifier{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:     repo,
		builder:  NewBuilder(),
		notifier: notifier,
		origin:   strings.TrimRight(origin, "/"),
		log:      log.With(slog.String("component", "recap")),
		now:      time.Now,
	}
}

// WithBuilder replaces the builder (city rank thresholds from config).
func (s *Service) WithBuilder(b *Builder) *Service {
	s.builder = b
	return s
}

// ShareURL is the public page for a share code.
func (s *Service) ShareURL(code string) string {
	return s.origin + "/r/" + code
}

// TripRecap returns the recap of one of userID's trips, building it when it
// is missing, not ready, or the owner asked for a rebuild while that is
// still allowed.
func (s *Service) TripRecap(ctx context.Context, userID, tripID uuid.UUID, refresh bool) (*recapv1.Recap, error) {
	now := s.now()
	row, err := s.repo.ByTrip(ctx, userID, tripID)
	switch {
	case err == nil:
		if row.Status == StatusReady && (!refresh || !row.Refreshable(now)) {
			return s.view(row, now)
		}
		if row.ShareCode != "" {
			return s.view(row, now)
		}
	case !errors.Is(err, ErrNotFound):
		return nil, err
	}

	c, err := s.repo.TripCandidate(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	start, end, ended := c.Ended(now)
	if !ended {
		return &recapv1.Recap{
			Kind:             recapv1.RecapKind_RECAP_KIND_TRIP,
			Status:           recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE,
			TripId:           tripID.String(),
			IneligibleReason: ReasonNotEnded,
			IsOwner:          true,
		}, nil
	}
	status, payload, reason, err := s.build(ctx, userID, tripID, start, end)
	if err != nil {
		return nil, err
	}
	row, err = s.repo.UpsertTrip(ctx, userID, tripID, start, end, status, payload, reason)
	if err != nil {
		return nil, err
	}
	return s.view(row, now)
}

// build loads facts and builds; status is ready or ineligible.
func (s *Service) build(ctx context.Context, userID, tripID uuid.UUID, start, end time.Time) (Status, []byte, string, error) {
	facts, err := s.repo.LoadTripFacts(ctx, userID, tripID, start, end)
	if err != nil {
		return 0, nil, "", err
	}
	rec, reason, ok := s.builder.BuildTrip(facts)
	if !ok {
		return StatusIneligible, nil, reason, nil
	}
	payload, err := proto.Marshal(rec)
	if err != nil {
		return 0, nil, "", fmt.Errorf("marshal recap: %w", err)
	}
	return StatusReady, payload, "", nil
}

// YearInReview is not built yet: it unlocks in December.
func (s *Service) YearInReview(_ context.Context, _ uuid.UUID, year int) (*recapv1.Recap, error) {
	return &recapv1.Recap{
		Kind:             recapv1.RecapKind_RECAP_KIND_YEAR,
		Status:           recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE,
		Year:             int32(year),
		IneligibleReason: ReasonYearLocked,
		IsOwner:          true,
	}, nil
}

// Get opens one of userID's recaps by id.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*recapv1.Recap, error) {
	row, err := s.repo.ByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.view(row, s.now())
}

// Shared opens a recap by share code, as a stranger sees it. countView is
// set for the page, not for its images.
func (s *Service) Shared(ctx context.Context, code string, countView bool) (*recapv1.Recap, error) {
	row, err := s.repo.ByShareCode(ctx, code, countView)
	if err != nil {
		return nil, err
	}
	rec, err := s.view(row, s.now())
	if err != nil {
		return nil, err
	}
	pub := PublicView(rec)
	pub.IsOwner = false
	return pub, nil
}

// List is userID's ready recaps.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]*recapv1.RecapSummary, time.Time, error) {
	rows, err := s.repo.List(ctx, userID, limit+1, before)
	if err != nil {
		return nil, time.Time{}, err
	}
	var next time.Time
	if len(rows) > limit {
		rows = rows[:limit]
		if r := rows[limit-1]; r.ReadyAt != nil {
			next = *r.ReadyAt
		}
	}
	out := make([]*recapv1.RecapSummary, 0, len(rows))
	for _, r := range rows {
		rec, err := decode(r.Payload)
		if err != nil {
			s.log.WarnContext(ctx, "undecodable recap skipped", slog.String("recap_id", r.ID.String()), slog.Any("error", err))
			continue
		}
		sum := &recapv1.RecapSummary{
			Id:          r.ID.String(),
			Kind:        recapv1.RecapKind(r.Kind),
			Year:        int32(r.Year),
			Title:       rec.GetTitle(),
			Subtitle:    rec.GetSubtitle(),
			PeriodLabel: rec.GetPeriodLabel(),
			Viewed:      r.ViewedAt != nil,
			Shared:      r.ShareCode != "",
		}
		if r.TripID != uuid.Nil {
			sum.TripId = r.TripID.String()
		}
		if r.ReadyAt != nil {
			sum.ReadyAt = timestamppb.New(*r.ReadyAt)
		}
		out = append(out, sum)
	}
	return out, next, nil
}

// Share returns the recap's share code and public URL, minting one if needed.
func (s *Service) Share(ctx context.Context, userID, id uuid.UUID) (code, url string, err error) {
	fresh, err := newShareCode()
	if err != nil {
		return "", "", err
	}
	code, err = s.repo.Share(ctx, userID, id, fresh)
	if errors.Is(err, ErrNotFound) {
		// Distinguish "not yours" from "not ready" for the caller.
		if _, gerr := s.repo.ByID(ctx, userID, id); gerr == nil {
			return "", "", ErrNotReady
		}
	}
	if err != nil {
		return "", "", err
	}
	return code, s.ShareURL(code), nil
}

// Unshare revokes the share code.
func (s *Service) Unshare(ctx context.Context, userID, id uuid.UUID) error {
	return s.repo.Unshare(ctx, userID, id)
}

// MarkViewed records that the owner opened the recap.
func (s *Service) MarkViewed(ctx context.Context, userID, id uuid.UUID) error {
	return s.repo.MarkViewed(ctx, userID, id)
}

// Owned is a ready recap of userID's, for rendering images of it.
func (s *Service) Owned(ctx context.Context, userID, id uuid.UUID) (*recapv1.Recap, error) {
	row, err := s.repo.ByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if row.Status != StatusReady {
		return nil, ErrNotReady
	}
	return s.view(row, s.now())
}

// view decodes a row and fills in what is not part of the snapshot.
func (s *Service) view(row Row, now time.Time) (*recapv1.Recap, error) {
	rec := &recapv1.Recap{}
	if row.Status == StatusReady {
		var err error
		rec, err = decode(row.Payload)
		if err != nil {
			return nil, err
		}
	}
	rec.Id = row.ID.String()
	rec.Kind = recapv1.RecapKind(row.Kind)
	rec.Status = recapv1.RecapStatus(row.Status)
	rec.Year = int32(row.Year)
	if row.TripID != uuid.Nil {
		rec.TripId = row.TripID.String()
	}
	rec.IneligibleReason = row.IneligibleReason
	rec.IsOwner = true
	rec.Refreshable = row.Refreshable(now)
	if row.ShareCode != "" {
		rec.ShareUrl = s.ShareURL(row.ShareCode)
	}
	if row.ReadyAt != nil {
		rec.ReadyAt = timestamppb.New(*row.ReadyAt)
	}
	if row.ViewedAt != nil {
		rec.ViewedAt = timestamppb.New(*row.ViewedAt)
	}
	return rec, nil
}

func decode(payload []byte) (*recapv1.Recap, error) {
	rec := &recapv1.Recap{}
	if err := proto.Unmarshal(payload, rec); err != nil {
		return nil, fmt.Errorf("decode recap: %w", err)
	}
	return rec, nil
}

// newShareCode is 9 random bytes, base64url: 12 characters, like invite codes.
func newShareCode() (string, error) {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("share code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Runner limits per tick.
const (
	candidateBatch = 200
	buildBatch     = 50
	notifyBatch    = 100
)

// TickResult counts what one runner tick did, for the log line.
type TickResult struct {
	Queued, Built, Ineligible, Failed, Notified int
}

// Tick does one round of the runner's work: queue trips that just ended,
// build what is pending, push what is ready.
func (s *Service) Tick(ctx context.Context) (TickResult, error) {
	var res TickResult
	now := s.now()

	// Wide UTC bounds; Due decides in each traveller's own zone.
	since := now.AddDate(0, 0, -(LookbackDays + 1))
	until := now.AddDate(0, 0, 1)
	cands, err := s.repo.Candidates(ctx, since, until, candidateBatch)
	if err != nil {
		return res, err
	}
	for _, c := range cands {
		start, end, due := c.Due(now)
		if !due {
			continue
		}
		ok, err := s.repo.Queue(ctx, c, start, end)
		if err != nil {
			return res, err
		}
		if ok {
			res.Queued++
		}
	}

	pending, err := s.repo.Pending(ctx, now, buildBatch)
	if err != nil {
		return res, err
	}
	for _, row := range pending {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		status, payload, reason, err := s.build(ctx, row.UserID, row.TripID, row.PeriodStart, row.PeriodEnd)
		if err != nil {
			attempts := row.Attempts + 1
			failed := attempts >= MaxAttempts
			// 10 min, 40 min: long enough for a flaky dependency to recover.
			retry := now.Add(time.Duration(attempts*attempts) * 10 * time.Minute)
			if serr := s.repo.SaveFailure(ctx, row.ID, err.Error(), retry, failed); serr != nil {
				return res, serr
			}
			s.log.WarnContext(ctx, "recap build failed", slog.String("recap_id", row.ID.String()),
				slog.Int("attempt", attempts), slog.Any("error", err))
			if failed {
				res.Failed++
			}
			continue
		}
		if err := s.repo.SaveBuilt(ctx, row.ID, status, payload, reason); err != nil {
			return res, err
		}
		if status == StatusReady {
			res.Built++
		} else {
			res.Ineligible++
		}
	}

	notices, err := s.repo.Unnotified(ctx, notifyBatch)
	if err != nil {
		return res, err
	}
	for _, n := range notices {
		// Someone who already opened it needs no push; close it quietly.
		if !n.Viewed && !InPushWindow(now, n.Timezone) {
			continue
		}
		claimed, err := s.repo.ClaimNotify(ctx, n.RecapID)
		if err != nil {
			return res, err
		}
		if !claimed || n.Viewed {
			continue
		}
		rec, err := decode(n.Payload)
		if err != nil {
			continue
		}
		title, body := PushText(rec)
		s.notifier.RecapReady(ctx, n.UserID, n.TripID, title, body)
		res.Notified++
	}
	return res, nil
}

// PushText is the notification for a ready trip recap.
func PushText(rec *recapv1.Recap) (title, body string) {
	title = "Your " + rec.GetTitle() + " recap is ready"
	body = "Tap to relive the trip."
	for _, c := range rec.GetCards() {
		if o := c.GetOutro(); o != nil && o.GetSummaryLine() != "" {
			body = o.GetSummaryLine() + ". Tap to relive the trip."
		}
	}
	return title, body
}
