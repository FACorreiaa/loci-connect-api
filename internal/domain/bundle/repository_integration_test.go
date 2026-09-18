//go:build integration

package bundle

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var (
	testDB   *pgxpool.Pool
	testRepo *RepositoryImpl
)

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	testRepo = NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func ctx() context.Context { return context.Background() }

// seedBundle writes a pack with `days` days of two stops each.
func seedBundle(t *testing.T, slug string, paid bool, status Status, days int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testDB.QueryRow(ctx(), `
        INSERT INTO bundles (slug, title, summary, city_name, country_code, theme,
                             months, day_count, stop_count, is_paid, status, published_at)
        VALUES ($1, $2, 'summary', 'Lisbon', 'PT', 'food', $3, $4, $5, $6, $7, now())
        RETURNING id`,
		slug, "Pack "+slug, []int16{9, 10}, days, days*2, paid, string(status),
	).Scan(&id)
	require.NoError(t, err)

	for d := 1; d <= days; d++ {
		var dayID uuid.UUID
		require.NoError(t, testDB.QueryRow(ctx(), `
            INSERT INTO bundle_days (bundle_id, day_number, title)
            VALUES ($1, $2, $3) RETURNING id`,
			id, d, fmt.Sprintf("Day %d", d)).Scan(&dayID))

		for s := 0; s < 2; s++ {
			_, err := testDB.Exec(ctx(), `
                INSERT INTO bundle_stops (bundle_day_id, order_index, name, latitude, longitude)
                VALUES ($1, $2, $3, 38.72, -9.14)`,
				dayID, s, fmt.Sprintf("day%d-stop%d", d, s))
			require.NoError(t, err)
		}
	}
	return id
}

func seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx(), `
        INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
		fmt.Sprintf("packs-%s@example.test", uuid.NewString()[:8]),
	).Scan(&id))
	return id
}

func TestListPublished_OnlyPublished(t *testing.T) {
	pub := seedBundle(t, "pub-"+uuid.NewString()[:8], false, StatusPublished, 2)
	draft := seedBundle(t, "draft-"+uuid.NewString()[:8], false, StatusDraft, 2)

	got, total, err := testRepo.ListPublished(ctx(), ListFilter{PageSize: 100})
	require.NoError(t, err)
	assert.Positive(t, total)

	ids := map[uuid.UUID]bool{}
	for _, b := range got {
		ids[b.ID] = true
	}
	assert.True(t, ids[pub], "published pack must be listed")
	assert.False(t, ids[draft], "a draft must never reach the catalog")
}

func TestListPublished_MonthFilterIncludesSeasonlessPacks(t *testing.T) {
	seasonless := seedBundle(t, "any-"+uuid.NewString()[:8], false, StatusPublished, 1)
	_, err := testDB.Exec(ctx(), "UPDATE bundles SET months = '{}' WHERE id = $1", seasonless)
	require.NoError(t, err)

	// January: the seeded packs are tagged Sep/Oct, so only the seasonless one
	// should match. A pack with no season is not out of season.
	got, _, err := testRepo.ListPublished(ctx(), ListFilter{Month: 1, PageSize: 100})
	require.NoError(t, err)

	found := false
	for _, b := range got {
		if b.ID == seasonless {
			found = true
		}
		assert.NotContains(t, b.Months, int16(9),
			"a Sep/Oct pack must not match a January filter")
	}
	assert.True(t, found, "a pack with no months must match any month")
}

// The paywall: locked days must not leave the database at all.
func TestLoadDays_MaxDaysTruncatesInSQL(t *testing.T) {
	id := seedBundle(t, "paid-"+uuid.NewString()[:8], true, StatusPublished, 4)

	preview, err := testRepo.LoadDays(ctx(), id, 1)
	require.NoError(t, err)
	require.Len(t, preview, 1, "an unowned caller gets day 1 and nothing else")
	assert.Equal(t, 1, preview[0].DayNumber)
	assert.Len(t, preview[0].Stops, 2, "day 1 arrives complete, so the map renders")

	full, err := testRepo.LoadDays(ctx(), id, 0)
	require.NoError(t, err)
	assert.Len(t, full, 4, "an owner gets every day")
	for _, d := range full {
		assert.Len(t, d.Stops, 2)
	}
}

func TestOwnership_IsNotGrantedByAnythingButAPurchase(t *testing.T) {
	user := seedUser(t)
	id := seedBundle(t, "own-"+uuid.NewString()[:8], true, StatusPublished, 3)

	owned, err := testRepo.IsOwned(ctx(), user, id)
	require.NoError(t, err)
	assert.False(t, owned, "nobody owns a pack they have not bought")

	session := "cs_test_" + uuid.NewString()[:12]
	p := Purchase{
		UserID: user, BundleID: id,
		StripeCheckoutSessionID: session,
		StripePaymentIntentID:   "pi_" + uuid.NewString()[:12],
		AmountCents:             499, Currency: "usd",
	}
	require.NoError(t, testRepo.RecordPurchase(ctx(), p))

	owned, err = testRepo.IsOwned(ctx(), user, id)
	require.NoError(t, err)
	assert.True(t, owned)

	// A replayed webhook must not double-grant or error.
	require.NoError(t, testRepo.RecordPurchase(ctx(), p))
	var count int
	require.NoError(t, testDB.QueryRow(ctx(),
		"SELECT COUNT(*) FROM bundle_purchases WHERE user_id = $1 AND bundle_id = $2",
		user, id).Scan(&count))
	assert.Equal(t, 1, count, "a replayed delivery must grant exactly once")

	// A second purchase of the same pack through a different session must not
	// create a second live entitlement either.
	p2 := p
	p2.StripeCheckoutSessionID = "cs_test_" + uuid.NewString()[:12]
	require.NoError(t, testRepo.RecordPurchase(ctx(), p2))
	require.NoError(t, testDB.QueryRow(ctx(),
		"SELECT COUNT(*) FROM bundle_purchases WHERE user_id = $1 AND bundle_id = $2 AND status = 'paid'",
		user, id).Scan(&count))
	assert.Equal(t, 1, count, "the partial unique index must hold")

	require.NoError(t, testRepo.MarkRefunded(ctx(), p.StripePaymentIntentID))
	owned, err = testRepo.IsOwned(ctx(), user, id)
	require.NoError(t, err)
	assert.False(t, owned, "a refund revokes access")
}

// A paid pack must survive the POI it points at being deleted or merged away.
func TestStopsSurvivePOIDeletion(t *testing.T) {
	var cityID uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx(), `
        INSERT INTO cities (name, country) VALUES ($1, 'Portugal') RETURNING id`,
		"PackTestCity-"+uuid.NewString()[:8]).Scan(&cityID))

	var poiID uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx(), `
        INSERT INTO points_of_interest (name, location, city_id)
        VALUES ('Time Out Market', ST_SetSRID(ST_MakePoint(-9.14, 38.72), 4326), $1)
        RETURNING id`, cityID).Scan(&poiID))

	id := seedBundle(t, "rot-"+uuid.NewString()[:8], true, StatusPublished, 1)
	var dayID uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx(),
		"SELECT id FROM bundle_days WHERE bundle_id = $1", id).Scan(&dayID))
	_, err := testDB.Exec(ctx(), `
        INSERT INTO bundle_stops (bundle_day_id, order_index, poi_id, name, latitude, longitude)
        VALUES ($1, 9, $2, 'Time Out Market', 38.72, -9.14)`, dayID, poiID)
	require.NoError(t, err)

	// A POI merge deletes the losing row. That must not take paid content with
	// it, and must not fail: bundle_stops.poi_id is ON DELETE SET NULL.
	_, err = testDB.Exec(ctx(), "DELETE FROM points_of_interest WHERE id = $1", poiID)
	require.NoError(t, err, "deleting a POI must not be blocked by a pack")

	days, err := testRepo.LoadDays(ctx(), id, 0)
	require.NoError(t, err)
	require.Len(t, days, 1)

	var found *Stop
	for i := range days[0].Stops {
		if days[0].Stops[i].Name == "Time Out Market" {
			found = &days[0].Stops[i]
		}
	}
	require.NotNil(t, found, "the stop must still be there after its POI is gone")
	assert.Nil(t, found.POIID, "the link is dropped")
	require.NotNil(t, found.Latitude)
	assert.InDelta(t, 38.72, *found.Latitude, 0.001, "the snapshot still renders the map")
}

// Deleting a pack somebody paid for must fail loudly rather than erase it.
func TestPurchasedBundleCannotBeDeleted(t *testing.T) {
	user := seedUser(t)
	id := seedBundle(t, "keep-"+uuid.NewString()[:8], true, StatusPublished, 1)
	require.NoError(t, testRepo.RecordPurchase(ctx(), Purchase{
		UserID: user, BundleID: id,
		StripeCheckoutSessionID: "cs_test_" + uuid.NewString()[:12],
		AmountCents:             499, Currency: "usd",
	}))

	_, err := testDB.Exec(ctx(), "DELETE FROM bundles WHERE id = $1", id)
	require.Error(t, err, "ON DELETE RESTRICT must refuse to erase a bought pack")
}
