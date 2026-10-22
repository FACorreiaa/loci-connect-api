package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	achievementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/achievement"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/achievements"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
)

// ErrInvalid is a malformed argument.
var ErrInvalid = errors.New("invalid argument")

// FeedKind is what a feed event says happened. Values match FeedItemKind.
type FeedKind int16

const (
	FeedAchievement  FeedKind = 1
	FeedNewCity      FeedKind = 2
	FeedTripFinished FeedKind = 3
	FeedTripShared   FeedKind = 4
)

const (
	defaultFeedPage = 20
	maxFeedPage     = 50
	// recentCheerers is how many of the latest cheerers an item shows.
	recentCheerers = 3
	// cheerPushesPerDay caps the cheer pushes one person gets a day.
	cheerPushesPerDay = 3
	// feedWriteTimeout bounds a feed write made beside the action it reports.
	feedWriteTimeout = 3 * time.Second
)

// FeedEvent is an event to record. A repeat of the same actor, kind and ref
// key records nothing, except that an achievement with a higher Rank replaces
// the day's lesser one.
type FeedEvent struct {
	Actor   uuid.UUID
	Kind    FeedKind
	RefKey  string
	TripID  *uuid.UUID
	Payload any
	Rank    int
}

// FeedTripInfo is a trip event's trip as it stands now.
type FeedTripInfo struct {
	Title    string
	CityName string
	Days     int32
}

// FeedRow is an event as a viewer reads it.
type FeedRow struct {
	ID          uuid.UUID
	Actor       uuid.UUID
	Kind        FeedKind
	Payload     []byte
	TripID      *uuid.UUID
	Trip        FeedTripInfo
	CityName    string
	Country     string
	CreatedAt   time.Time
	Cheers      int32
	CheeredByMe bool
}

// feedCursor is where a feed page ends: the last item's time and id.
type feedCursor struct {
	At time.Time
	ID uuid.UUID
}

// FeedRepository stores the feed and its cheers.
type FeedRepository interface {
	Post(ctx context.Context, e FeedEvent) error
	// Feed is what viewer may see from the people they follow, newest first,
	// after the cursor when there is one.
	Feed(ctx context.Context, viewer uuid.UUID, after *feedCursor, limit int) ([]FeedRow, error)
	// Event is one item of viewer's feed, ErrNotFound when it is not in it.
	Event(ctx context.Context, viewer, id uuid.UUID) (FeedRow, error)
	RecentCheerers(ctx context.Context, ids []uuid.UUID, per int) (map[uuid.UUID][]uuid.UUID, error)
	// Cheer reports whether the cheer is new.
	Cheer(ctx context.Context, id, userID uuid.UUID) (bool, error)
	Uncheer(ctx context.Context, id, userID uuid.UUID) error
	CheerState(ctx context.Context, id, userID uuid.UUID) (count int32, mine bool, err error)
	// ClaimCheerNotice reports whether the event's owner is yet to be told
	// about a cheer, and marks them told.
	ClaimCheerNotice(ctx context.Context, id uuid.UUID) (bool, error)
	Timezone(ctx context.Context, userID uuid.UUID) (string, error)
}

// CheerNotifier tells someone their feed item was cheered. Best effort.
type CheerNotifier interface {
	Cheered(ctx context.Context, to uuid.UUID, from *socialv1.PublicUser, what, path string)
}

// Feed is the following feed: what the people a viewer follows did, and the
// cheers on it. Its Post methods are safe on a nil feed, so the packages that
// write events need no check when the feed is off.
type Feed struct {
	repo   FeedRepository
	people Repository
	notify CheerNotifier
	log    *slog.Logger
	now    func() time.Time
}

// NewFeed builds the feed. people supplies cards and push slots; a nil
// notifier sends nothing.
func NewFeed(repo FeedRepository, people Repository, notify CheerNotifier, log *slog.Logger) *Feed {
	if log == nil {
		log = slog.Default()
	}
	return &Feed{repo: repo, people: people, notify: notify, log: log.With(slog.String("component", "feed")), now: time.Now}
}

// achievementPayload is an achievement event's detail, stored as rendered
// when earned.
type achievementPayload struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Kind        int32     `json:"kind"`
	Tier        int32     `json:"tier"`
	EarnedAt    time.Time `json:"earned_at"`
	Rank        int       `json:"rank"`
}

// cityPayload is a new city's detail. CityID is left out when the city has
// no row, which the reader joins on for its name and country.
type cityPayload struct {
	CityID *uuid.UUID `json:"city_id,omitempty"`
	Name   string     `json:"city_name"`
}

func (f *Feed) post(ctx context.Context, e FeedEvent) {
	if f == nil || e.Actor == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), feedWriteTimeout)
	defer cancel()
	if err := f.repo.Post(ctx, e); err != nil {
		f.log.WarnContext(ctx, "feed write failed",
			slog.String("user_id", e.Actor.String()), slog.Int("kind", int(e.Kind)), slog.Any("error", err))
	}
}

// PostAchievement records the day's best unlock: the first of a local day,
// or a better one (higher rank) than the day's so far.
func (f *Feed) PostAchievement(ctx context.Context, actor uuid.UUID, a *achievementv1.AchievementSummary, rank int, day time.Time) {
	if a == nil {
		return
	}
	earned := f.now()
	if a.GetEarnedAt() != nil {
		earned = a.GetEarnedAt().AsTime()
	}
	f.post(ctx, FeedEvent{
		Actor: actor, Kind: FeedAchievement, RefKey: "achievements:" + day.Format(time.DateOnly), Rank: rank,
		Payload: achievementPayload{
			ID: a.GetId(), Title: a.GetTitle(), Description: a.GetDescription(), Icon: a.GetIcon(),
			Kind: int32(a.GetKind()), Tier: int32(a.GetTier()), EarnedAt: earned, Rank: rank,
		},
	})
}

// PostNewCity records the first time actor reached a city; key is the
// award's city key, so a city counts once whatever names it goes by.
func (f *Feed) PostNewCity(ctx context.Context, actor uuid.UUID, key string, cityID *uuid.UUID, name string) {
	f.post(ctx, FeedEvent{
		Actor: actor, Kind: FeedNewCity, RefKey: "city:" + key,
		Payload: cityPayload{CityID: cityID, Name: name},
	})
}

// PostTripFinished records actor finishing a trip. Followers see it only while
// the trip is visible to them.
func (f *Feed) PostTripFinished(ctx context.Context, actor, tripID uuid.UUID) {
	f.post(ctx, FeedEvent{Actor: actor, Kind: FeedTripFinished, RefKey: "trip:" + tripID.String(), TripID: &tripID, Payload: struct{}{}})
}

// PostTripShared records the first time actor opened a trip to friends or
// to everyone.
func (f *Feed) PostTripShared(ctx context.Context, actor, tripID uuid.UUID) {
	f.post(ctx, FeedEvent{Actor: actor, Kind: FeedTripShared, RefKey: "trip:" + tripID.String(), TripID: &tripID, Payload: struct{}{}})
}

// FeedItem is a row with its people as cards.
type FeedItem struct {
	FeedRow
	Actor    *socialv1.PublicUser
	Cheerers []*socialv1.PublicUser
}

// List is a page of viewer's feed and the token of the next one.
func (f *Feed) List(ctx context.Context, viewer uuid.UUID, pageSize int, pageToken string) ([]FeedItem, string, error) {
	if pageSize <= 0 {
		pageSize = defaultFeedPage
	}
	pageSize = min(pageSize, maxFeedPage)
	var after *feedCursor
	if pageToken != "" {
		c, err := decodeFeedCursor(pageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: page token", ErrInvalid)
		}
		after = &c
	}
	rows, err := f.repo.Feed(ctx, viewer, after, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		last := rows[len(rows)-1]
		next = encodeFeedCursor(feedCursor{At: last.CreatedAt, ID: last.ID})
	}
	if len(rows) == 0 {
		return nil, "", nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	cheerers, err := f.repo.RecentCheerers(ctx, ids, recentCheerers)
	if err != nil {
		return nil, "", err
	}
	people := map[uuid.UUID]bool{}
	for _, r := range rows {
		people[r.Actor] = true
		for _, c := range cheerers[r.ID] {
			people[c] = true
		}
	}
	all := make([]uuid.UUID, 0, len(people))
	for id := range people {
		all = append(all, id)
	}
	cards, err := f.people.PublicUsers(ctx, all)
	if err != nil {
		return nil, "", err
	}
	out := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		actor := cards[r.Actor]
		if actor == nil {
			continue
		}
		item := FeedItem{FeedRow: r, Actor: actor}
		for _, c := range cheerers[r.ID] {
			if card := cards[c]; card != nil {
				item.Cheerers = append(item.Cheerers, card)
			}
		}
		out = append(out, item)
	}
	return out, next, nil
}

// Cheer adds viewer's cheer to an item of their feed. The item's owner hears
// about the first cheer it ever gets, unless it is night where they are or
// they had their three cheer pushes today.
func (f *Feed) Cheer(ctx context.Context, viewer, id uuid.UUID) (int32, bool, error) {
	ev, err := f.repo.Event(ctx, viewer, id)
	if err != nil {
		return 0, false, err
	}
	added, err := f.repo.Cheer(ctx, id, viewer)
	if err != nil {
		return 0, false, err
	}
	if added {
		f.announceCheer(ctx, viewer, ev)
	}
	return f.repo.CheerState(ctx, id, viewer)
}

// Uncheer takes viewer's cheer back.
func (f *Feed) Uncheer(ctx context.Context, viewer, id uuid.UUID) (int32, bool, error) {
	if err := f.repo.Uncheer(ctx, id, viewer); err != nil {
		return 0, false, err
	}
	return f.repo.CheerState(ctx, id, viewer)
}

func (f *Feed) announceCheer(ctx context.Context, from uuid.UUID, ev FeedRow) {
	if f.notify == nil {
		return
	}
	tz, err := f.repo.Timezone(ctx, ev.Actor)
	if err != nil || gamification.Quiet(f.now(), tz) {
		return
	}
	first, err := f.repo.ClaimCheerNotice(ctx, ev.ID)
	if err != nil || !first {
		return
	}
	claimed := false
	for i := 1; i <= cheerPushesPerDay && !claimed; i++ {
		ok, err := f.people.ClaimPushSlot(ctx, ev.Actor, "cheer:"+strconv.Itoa(i))
		if err != nil {
			f.log.WarnContext(ctx, "claim cheer push slot", slog.String("user_id", ev.Actor.String()), slog.Any("error", err))
			return
		}
		claimed = ok
	}
	if !claimed {
		return
	}
	cards, err := f.people.PublicUsers(ctx, []uuid.UUID{from})
	if err != nil || cards[from] == nil {
		return
	}
	what, path := cheeredWhat(ev)
	f.notify.Cheered(ctx, ev.Actor, cards[from], what, path)
}

// cheeredWhat names the item in a cheer push ("your Cartographer · Gold")
// and the app path it opens.
func cheeredWhat(ev FeedRow) (string, string) {
	switch ev.Kind {
	case FeedAchievement:
		var a achievementPayload
		_ = json.Unmarshal(ev.Payload, &a)
		title := a.Title
		if name, ok := achievements.TierNames[achievements.Tier(a.Tier)]; ok {
			title += " · " + name
		}
		return title, "/achievements"
	case FeedNewCity:
		return "new city: " + ev.CityName, "/profile"
	default:
		if ev.Trip.Title != "" {
			return "trip " + ev.Trip.Title, "/profile"
		}
		return "trip", "/profile"
	}
}

func encodeFeedCursor(c feedCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.At.UnixMicro(), 10) + ":" + c.ID.String()))
}

func decodeFeedCursor(s string) (feedCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return feedCursor{}, err
	}
	at, id, ok := strings.Cut(string(b), ":")
	if !ok {
		return feedCursor{}, errors.New("malformed cursor")
	}
	micros, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return feedCursor{}, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return feedCursor{}, err
	}
	return feedCursor{At: time.UnixMicro(micros).UTC(), ID: u}, nil
}
