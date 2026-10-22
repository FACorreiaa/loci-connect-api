package achievements

import (
	"context"
	"log/slog"
	"time"

	achievementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/achievement"
	gamificationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
)

// Notifier announces an unlock on the user's devices. Best effort.
type Notifier interface {
	AchievementUnlocked(ctx context.Context, to uuid.UUID, title, description string)
}

// Viewer decides whether viewer may see owner's achievements.
// social.Service satisfies it.
type Viewer interface {
	CanView(ctx context.Context, viewer, owner uuid.UUID) (visible, blocked bool, err error)
}

const (
	// pushKind is the once-a-day slot an unlock push takes.
	pushKind = "achievement"
	// rarityMinBase is how many active explorers rarity needs before it
	// means anything; below it the apps show none.
	rarityMinBase = 50
	// rarityWindow is how far back an explorer counts as active.
	rarityWindow = 365 * 24 * time.Hour
	// rarityEvery is how stale the holder counts may get.
	rarityEvery = time.Hour
	// touchTimeout bounds an evaluation made off the request path.
	touchTimeout = 5 * time.Second
)

// Service judges and lists achievements.
type Service struct {
	repo    Repository
	notify  Notifier
	viewer  Viewer
	log     *slog.Logger
	now     func() time.Time
	enabled bool
	// keeperScore is the city score the Keeper rank starts at.
	keeperScore int64
}

// NewService builds the service. With enabled false nothing is granted and
// the list is empty: the kill switch.
func NewService(repo Repository, notify Notifier, viewer Viewer, log *slog.Logger, enabled bool) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:        repo,
		notify:      notify,
		viewer:      viewer,
		log:         log.With(slog.String("component", "achievements")),
		now:         time.Now,
		enabled:     enabled,
		keeperScore: gamification.DefaultCityThresholds[gamification.RankKeeper-1],
	}
}

// WithCityRanks takes the Keeper threshold from the city ranks in use.
func (s *Service) WithCityRanks(t gamification.Thresholds) *Service {
	s.keeperScore = t[gamification.RankKeeper-1]
	return s
}

// Due is what counts newly earns over what is held: every track tier
// reached, every moment and secret reached, that is not held yet.
func Due(counts map[Counter]int64, held map[Unit]Earned) []Unit {
	var out []Unit
	for _, d := range Catalogue {
		n := counts[d.Counter]
		if d.Kind != KindTrack {
			u := Unit{ID: d.ID}
			if _, ok := held[u]; !ok && n >= d.Threshold(TierNone) {
				out = append(out, u)
			}
			continue
		}
		for t := TierBronze; t <= TierPlatinum; t++ {
			u := Unit{ID: d.ID, Tier: t}
			if _, ok := held[u]; !ok && n >= d.Threshold(t) {
				out = append(out, u)
			}
		}
	}
	return out
}

func index(earned []Earned) map[Unit]Earned {
	out := make(map[Unit]Earned, len(earned))
	for _, e := range earned {
		out[e.Unit] = e
	}
	return out
}

// Evaluate grants what userID has newly earned and returns it, one entry per
// achievement at its highest tier. A user whose past was never evaluated is
// backfilled instead: everything is granted silently and nothing returned.
// A live unlock pushes at most once a day, never at night.
func (s *Service) Evaluate(ctx context.Context, userID uuid.UUID) ([]*achievementv1.Achievement, error) {
	if s == nil || !s.enabled || userID == uuid.Nil {
		return nil, nil
	}
	counts, err := s.repo.Counters(ctx, userID, s.keeperScore)
	if err != nil {
		return nil, err
	}
	earned, backfilled, err := s.repo.Earned(ctx, userID)
	if err != nil {
		return nil, err
	}
	held := index(earned)
	due := Due(counts, held)
	source := SourceLive
	if !backfilled {
		source = SourceBackfill
	} else if len(due) == 0 {
		return nil, nil
	}
	granted, err := s.repo.Grant(ctx, userID, due, source)
	if err != nil {
		return nil, err
	}
	if source == SourceBackfill || len(granted) == 0 {
		if source == SourceBackfill {
			s.log.DebugContext(ctx, "achievements backfilled", slog.String("user_id", userID.String()), slog.Int("count", len(granted)))
		}
		return nil, nil
	}
	now := s.now()
	ids := map[string]bool{}
	for _, u := range granted {
		held[u] = Earned{Unit: u, At: now, Source: SourceLive}
		ids[u.ID] = true
	}
	var out []*achievementv1.Achievement
	for _, d := range Catalogue {
		if ids[d.ID] {
			out = append(out, view(d, counts[d.Counter], held, Rarity{}, true))
		}
	}
	s.announce(ctx, userID, granted)
	return out, nil
}

// rank orders unlocks for the one push a day: a secret, then the highest
// track tier, then a moment.
func rank(u Unit) int {
	d, _ := ByID(u.ID)
	switch d.Kind {
	case KindSecret:
		return 10
	case KindTrack:
		return 1 + int(u.Tier)
	default:
		return 1
	}
}

func (s *Service) announce(ctx context.Context, userID uuid.UUID, granted []Unit) {
	if s.notify == nil {
		return
	}
	best := granted[0]
	for _, u := range granted[1:] {
		if rank(u) > rank(best) {
			best = u
		}
	}
	tz, err := s.repo.Timezone(ctx, userID)
	if err != nil {
		return
	}
	now := s.now()
	if gamification.Quiet(now, tz) {
		return
	}
	ok, err := s.repo.ClaimPushSlot(ctx, userID, pushKind, gamification.LocalDate(now, tz))
	if err != nil || !ok {
		return
	}
	d, _ := ByID(best.ID)
	title := d.Title
	if d.Kind == KindTrack {
		title += " · " + TierNames[best.Tier]
	}
	s.notify.AchievementUnlocked(ctx, userID, title, d.Describe(d.Threshold(best.Tier)))
}

// Touch evaluates userID off the request path, for actions that earn no
// points (a review, a board post, a diary entry). Safe on a nil service.
func (s *Service) Touch(ctx context.Context, userID uuid.UUID) {
	if s == nil || !s.enabled || userID == uuid.Nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), touchTimeout)
		defer cancel()
		if _, err := s.Evaluate(ctx, userID); err != nil {
			s.log.Warn("achievement check failed", slog.String("user_id", userID.String()), slog.Any("error", err))
		}
	}()
}

// List is owner's whole catalogue as viewer sees it. The owner's own list is
// evaluated first, which catches anything earned without points.
func (s *Service) List(ctx context.Context, viewer, owner uuid.UUID) (*gamificationv1.ListAchievementsResponse, error) {
	self := viewer == owner
	if !self && s.viewer != nil {
		visible, blocked, err := s.viewer.CanView(ctx, viewer, owner)
		if err != nil {
			return nil, err
		}
		if blocked {
			return nil, gamification.ErrNotFound
		}
		if !visible {
			return nil, gamification.ErrPrivate
		}
	}
	out := &gamificationv1.ListAchievementsResponse{Total: int32(TotalUnits())}
	if !s.enabled {
		return out, nil
	}
	if self {
		if _, err := s.Evaluate(ctx, owner); err != nil {
			s.log.Warn("achievement check failed", slog.String("user_id", owner.String()), slog.Any("error", err))
		}
	}
	counts, err := s.repo.Counters(ctx, owner, s.keeperScore)
	if err != nil {
		return nil, err
	}
	earned, _, err := s.repo.Earned(ctx, owner)
	if err != nil {
		return nil, err
	}
	rarity, err := s.repo.Rarity(ctx)
	if err != nil {
		return nil, err
	}
	held := index(earned)
	for _, d := range Catalogue {
		out.Achievements = append(out.Achievements, view(d, counts[d.Counter], held, rarity, self))
	}
	for _, e := range earned {
		if _, ok := ByID(e.ID); !ok {
			continue
		}
		out.EarnedCount++
		if self && e.Source == SourceBackfill && !e.Seen {
			out.UnseenBackfillCount++
		}
	}
	return out, nil
}

// MarkSeen clears the new mark on the user's achievements; no ids clears all.
func (s *Service) MarkSeen(ctx context.Context, userID uuid.UUID, ids []string) error {
	return s.repo.MarkSeen(ctx, userID, ids)
}

// BackfillBatch evaluates up to limit users whose past was never evaluated.
// Evaluation backfills them silently. It returns how many it evaluated.
func (s *Service) BackfillBatch(ctx context.Context, limit int) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	ids, err := s.repo.Unbackfilled(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if _, err := s.Evaluate(ctx, id); err != nil {
			s.log.WarnContext(ctx, "achievement backfill failed", slog.String("user_id", id.String()), slog.Any("error", err))
			continue
		}
		done++
	}
	return done, nil
}

// RefreshRarity recounts holders when the last count is an hour old.
func (s *Service) RefreshRarity(ctx context.Context) error {
	if !s.enabled {
		return nil
	}
	now := s.now()
	_, err := s.repo.RefreshRarity(ctx, now.Add(-rarityWindow), now.Add(-rarityEvery))
	return err
}

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// view is d as it stands for a user with counter n and units held. owner
// says whether the viewer is that user (only they see what is new).
func view(d Def, n int64, held map[Unit]Earned, rarity Rarity, owner bool) *achievementv1.Achievement {
	a := &achievementv1.Achievement{
		Id:       d.ID,
		Title:    d.Title,
		Icon:     d.Icon,
		Category: d.Category,
		Kind:     achievementv1.AchievementKind(d.Kind),
	}
	rarityOf := Unit{ID: d.ID}
	if d.Kind == KindTrack {
		top := TierNone
		for t := TierBronze; t <= TierPlatinum; t++ {
			step := &achievementv1.TierStep{Tier: achievementv1.AchievementTier(t), Threshold: d.Threshold(t)}
			if e, ok := held[Unit{ID: d.ID, Tier: t}]; ok {
				step.EarnedAt = ts(e.At)
				top = t
				a.EarnedAt = step.EarnedAt
				a.IsNew = a.IsNew || (owner && !e.Seen)
			}
			a.Steps = append(a.Steps, step)
		}
		a.Tier = achievementv1.AchievementTier(top)
		a.Progress = n
		target := d.Tiers[len(d.Tiers)-1]
		if top < TierPlatinum {
			a.NextTarget = d.Threshold(top + 1)
			target = a.NextTarget
		}
		a.Description = d.Describe(target)
		rarityOf.Tier = max(top, TierBronze)
	} else {
		e, ok := held[Unit{ID: d.ID}]
		if !ok && d.Kind == KindSecret {
			a.Title, a.Icon, a.Hidden = "", "lock", true
			return a
		}
		a.Description = d.Blurb
		target := d.Threshold(TierNone)
		a.Progress = min(n, target)
		if ok {
			a.Progress = target
			a.EarnedAt = ts(e.At)
			a.IsNew = owner && !e.Seen
		} else {
			a.NextTarget = target
		}
	}
	if rarity.Base >= rarityMinBase {
		pct := min(float32(rarity.Holders[rarityOf])*100/float32(rarity.Base), 100)
		a.RarityPct = &pct
	}
	return a
}
