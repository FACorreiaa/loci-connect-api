//go:build integration

package social

import (
	"context"
	"sync"
	"testing"
	"time"

	achievementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/achievement"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type cheerPushes struct {
	mu   sync.Mutex
	sent []string
}

func (c *cheerPushes) Cheered(_ context.Context, to uuid.UUID, from *socialv1.PublicUser, what, _ string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, to.String()+" "+from.GetId()+" "+what)
}

func (c *cheerPushes) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func feedTrip(t *testing.T, owner uuid.UUID, title string, vis int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, testSocialDB.QueryRow(context.Background(), `
		INSERT INTO trips (user_id, title, city_name, visibility) VALUES ($1, $2, 'Porto', $3) RETURNING id`,
		owner, title, vis).Scan(&id))
	_, err := testSocialDB.Exec(context.Background(), `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 1), ($1, 2)`, id)
	require.NoError(t, err)
	return id
}

func kinds(items []FeedItem) []FeedKind {
	out := make([]FeedKind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

// The feed on the real schema: what a follower sees, trip visibility re-read
// on every page, the day's best achievement, paging, cheers and their one
// push, and blocks.
func TestFeedEndToEnd(t *testing.T) {
	ctx := context.Background()
	people := NewRepository(testSocialDB)
	svc := NewService(people, nil, nil)
	pushes := &cheerPushes{}
	feed := NewFeed(NewFeedRepository(testSocialDB), people, pushes, nil)
	feed.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	ana, rui, eva := newSocialUser(t), newSocialUser(t), newSocialUser(t)

	_, _, _, err := svc.Follow(ctx, ana, rui)
	require.NoError(t, err)
	_, _, _, err = svc.Follow(ctx, eva, rui)
	require.NoError(t, err)

	porto := uuid.New()
	_, err = testSocialDB.Exec(ctx, `INSERT INTO cities (id, name, country) VALUES ($1, 'Porto', 'Portugal')`, porto)
	require.NoError(t, err)
	friendsTrip := feedTrip(t, rui, "Douro weekend", 2)
	publicTrip := feedTrip(t, rui, "Ribeira walk", 4)

	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	summary := func(id string, tier achievementv1.AchievementTier) *achievementv1.AchievementSummary {
		return &achievementv1.AchievementSummary{Id: id, Title: id, Tier: tier, EarnedAt: timestamppb.New(day)}
	}
	feed.PostNewCity(ctx, rui, porto.String(), &porto, "porto")
	feed.PostNewCity(ctx, rui, porto.String(), &porto, "porto") // once
	feed.PostTripFinished(ctx, rui, friendsTrip)
	feed.PostTripShared(ctx, rui, publicTrip)
	feed.PostAchievement(ctx, rui, summary("first-pin", 0), 1, day)
	feed.PostAchievement(ctx, rui, summary("trailhead", 2), 3, day)
	feed.PostAchievement(ctx, rui, summary("first-footstep", 0), 1, day) // lesser: kept out
	feed.PostAchievement(ctx, ana, summary("first-pin", 0), 1, day)      // Ana's own: not in her feed

	// One way: the friends-only trip stays hidden.
	items, next, err := feed.List(ctx, ana, 0, "")
	require.NoError(t, err)
	assert.Empty(t, next)
	assert.ElementsMatch(t, []FeedKind{FeedNewCity, FeedTripShared, FeedAchievement}, kinds(items))
	for _, it := range items {
		assert.Equal(t, rui.String(), it.Actor.GetId())
		switch it.Kind {
		case FeedNewCity:
			assert.Equal(t, "Porto", it.CityName, "the city row names it")
			assert.Equal(t, "Portugal", it.Country)
		case FeedTripShared:
			assert.Equal(t, FeedTripInfo{Title: "Ribeira walk", CityName: "Porto", Days: 2}, it.Trip)
		case FeedAchievement:
			p := feedItemProto(it)
			assert.Equal(t, "trailhead", p.GetAchievement().GetId(), "the day's best achievement")
			assert.Equal(t, achievementv1.AchievementTier_ACHIEVEMENT_TIER_SILVER, p.GetAchievement().GetTier())
		}
	}

	// Friends: now it shows.
	_, _, _, err = svc.Follow(ctx, rui, ana)
	require.NoError(t, err)
	items, _, err = feed.List(ctx, ana, 0, "")
	require.NoError(t, err)
	assert.Len(t, items, 4)

	// Paging walks every item once.
	seen := map[uuid.UUID]bool{}
	token := ""
	for page := 0; ; page++ {
		require.Less(t, page, 5)
		got, nextToken, err := feed.List(ctx, ana, 3, token)
		require.NoError(t, err)
		for _, it := range got {
			assert.False(t, seen[it.ID], "item repeated across pages")
			seen[it.ID] = true
		}
		if nextToken == "" {
			break
		}
		token = nextToken
	}
	assert.Len(t, seen, 4)
	_, _, err = feed.List(ctx, ana, 3, "not-a-token")
	assert.ErrorIs(t, err, ErrInvalid)

	// A trip made private again leaves the feed.
	_, err = testSocialDB.Exec(ctx, `UPDATE trips SET visibility = 1 WHERE id = $1`, publicTrip)
	require.NoError(t, err)
	items, _, err = feed.List(ctx, ana, 0, "")
	require.NoError(t, err)
	assert.Len(t, items, 3)

	// Cheers: counted, shown, and Rui hears about the first one only.
	var city FeedItem
	for _, it := range items {
		if it.Kind == FeedNewCity {
			city = it
		}
	}
	n, mine, err := feed.Cheer(ctx, ana, city.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), n)
	assert.True(t, mine)
	n, _, err = feed.Cheer(ctx, ana, city.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), n, "a cheer counted twice")
	n, mine, err = feed.Cheer(ctx, eva, city.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), n)
	assert.True(t, mine)
	n, mine, err = feed.Uncheer(ctx, ana, city.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), n)
	assert.False(t, mine)
	_, _, err = feed.Cheer(ctx, ana, city.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, pushes.count(), "cheers on one item pushed more than once: %v", pushes.sent)
	assert.Contains(t, pushes.sent[0], "new city: Porto")

	items, _, err = feed.List(ctx, eva, 0, "")
	require.NoError(t, err)
	for _, it := range items {
		if it.ID == city.ID {
			assert.Equal(t, int32(2), it.Cheers)
			assert.True(t, it.CheeredByMe)
			require.Len(t, it.Cheerers, 2)
			assert.Equal(t, ana.String(), it.Cheerers[0].GetId(), "latest cheerer first")
		}
	}

	// Someone who does not follow Rui cannot cheer him.
	stranger := newSocialUser(t)
	_, _, err = feed.Cheer(ctx, stranger, city.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// A block empties the feed both ways.
	require.NoError(t, svc.Block(ctx, rui, ana))
	items, _, err = feed.List(ctx, ana, 0, "")
	require.NoError(t, err)
	assert.Empty(t, items)
	_, _, err = feed.Cheer(ctx, ana, city.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// A deleted trip takes its events along.
	_, err = testSocialDB.Exec(ctx, `DELETE FROM trips WHERE id = $1`, friendsTrip)
	require.NoError(t, err)
	var left int
	require.NoError(t, testSocialDB.QueryRow(ctx, `SELECT COUNT(*) FROM feed_events WHERE trip_id = $1`, friendsTrip).Scan(&left))
	assert.Zero(t, left)
}

// Three cheer pushes a day at most, whoever cheers what.
func TestCheerPushesAreCappedPerDay(t *testing.T) {
	ctx := context.Background()
	people := NewRepository(testSocialDB)
	svc := NewService(people, nil, nil)
	pushes := &cheerPushes{}
	feed := NewFeed(NewFeedRepository(testSocialDB), people, pushes, nil)
	feed.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	star, fan := newSocialUser(t), newSocialUser(t)
	_, _, _, err := svc.Follow(ctx, fan, star)
	require.NoError(t, err)
	for range 5 {
		key := uuid.NewString()
		feed.PostNewCity(ctx, star, key, nil, "City "+key[:4])
	}
	items, _, err := feed.List(ctx, fan, 0, "")
	require.NoError(t, err)
	require.Len(t, items, 5)
	for _, it := range items {
		_, _, err := feed.Cheer(ctx, fan, it.ID)
		require.NoError(t, err)
	}
	assert.Equal(t, cheerPushesPerDay, pushes.count())

	// At night nothing is pushed and nothing is used up.
	night := NewFeed(NewFeedRepository(testSocialDB), people, pushes, nil)
	night.now = func() time.Time { return time.Date(2026, 10, 22, 23, 30, 0, 0, time.UTC) }
	other := newSocialUser(t)
	_, _, _, err = svc.Follow(ctx, other, fan)
	require.NoError(t, err)
	night.PostNewCity(ctx, fan, "late", nil, "Late")
	items, _, err = night.List(ctx, other, 0, "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, _, err = night.Cheer(ctx, other, items[0].ID)
	require.NoError(t, err)
	assert.Equal(t, cheerPushesPerDay, pushes.count())
	var notified *time.Time
	require.NoError(t, testSocialDB.QueryRow(ctx, `SELECT cheer_notified_at FROM feed_events WHERE id = $1`, items[0].ID).Scan(&notified))
	assert.Nil(t, notified, "a cheer at night used up the item's one push")
}
