//go:build integration

package compare

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type noPOIs struct{}

func (noPOIs) GetPOIsByCityID(context.Context, uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	return nil, nil
}

type freePlan struct{}

func (freePlan) EffectivePlan(context.Context, uuid.UUID) (string, error) { return "free", nil }

// The reported failure, reproduced end to end and then fixed.
//
//	POST /loci.compare.v1.CompareService/CompareWeekend
//	{"code":"invalid_argument","message":"compare: origin city not found: Porto"}
//
// Everything here is real except the weather and the transport stubs: a real
// Postgres with the real migrations, an empty cities table, the real repository
// and the real geocoder. That combination is the one that was broken, and it is
// the one no amount of faking can vouch for.
func TestCompareWeekend_WorksAgainstAnEmptyDatabase(t *testing.T) {
	if os.Getenv("LOCI_LIVE_GEOCODE") != "1" {
		t.Skip("set LOCI_LIVE_GEOCODE=1 to run compare against the real geocoder")
	}

	ctx := context.Background()
	pool := testsupport.MustPool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The exact condition that produced the bug: no rows for these cities.
	_, err := pool.Exec(ctx, "DELETE FROM cities WHERE name IN ('Porto', 'Evora', 'Évora', 'Beja')")
	require.NoError(t, err)

	repo := cityrepo.NewCityRepository(pool, logger)
	resolver := cityrepo.NewResolver(
		repo,
		geocode.NewOpenMeteo("", "", httpx.New(httpx.Config{}), nil),
		logger,
	)

	svc := NewService(
		resolver,
		noPOIs{},
		localcontext.StubWeather{},
		true,
		localcontext.StubTransportWithDrive{Fallback: localcontext.StubTransport{}},
		localcontext.BookingComDeepLink{},
		localcontext.OpenTableDeepLink{},
		freePlan{},
		logger,
	)
	handler := NewHandler(svc)

	origin := "Porto"
	start := time.Now().AddDate(0, 0, 3)
	resp, err := handler.CompareWeekend(ctx, connect.NewRequest(&comparev1.CompareWeekendRequest{
		OriginCity:         &origin,
		CandidateCityNames: []string{"Évora", "Beja"},
		StartDate:          timestamppb.New(start),
		EndDate:            timestamppb.New(start.Add(48 * time.Hour)),
	}))
	require.NoError(t, err, "this is the request that returned 400 invalid_argument")

	msg := resp.Msg
	assert.Equal(t, "Porto", msg.OriginCity)
	assert.InDelta(t, 41.15, msg.OriginLat, 1.0)
	require.Len(t, msg.Columns, 2)

	for _, col := range msg.Columns {
		assert.NotEmpty(t, col.CityName)
		assert.NotZero(t, col.CenterLat, "a column without coordinates cannot be compared")
		assert.Greater(t, col.DistanceKm, 0.0)
		assert.Greater(t, col.TravelMins, int32(0))
		assert.NotNil(t, col.GoScore, "the go/no-go verdict is the point of the page")
		assert.NotEmpty(t, col.Pros)
		assert.NotEmpty(t, col.Cons)
	}

	// Both candidates are in the Alentejo, a few hours from Porto. Anything
	// over a thousand kilometres means a same-named city on another continent
	// won the ranking.
	for _, col := range msg.Columns {
		assert.Less(t, col.DistanceKm, 1000.0,
			"%s resolved to somewhere that is not a weekend from Porto", col.CityName)
	}

	// The comparison also left the gazetteer better than it found it.
	var stored int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM cities
		WHERE name IN ('Porto', 'Evora', 'Évora', 'Beja')
		  AND center_location IS NOT NULL`).Scan(&stored))
	assert.Equal(t, 3, stored, "each resolved city should have been persisted with a position")
}

// A name that is not a place is the caller's to fix; that much of the old
// behaviour was right.
func TestCompareWeekend_UnresolvableCitiesAreStillInvalidArgument(t *testing.T) {
	if os.Getenv("LOCI_LIVE_GEOCODE") != "1" {
		t.Skip("set LOCI_LIVE_GEOCODE=1 to run compare against the real geocoder")
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := testsupport.MustPool()

	resolver := cityrepo.NewResolver(
		cityrepo.NewCityRepository(pool, logger),
		geocode.NewOpenMeteo("", "", httpx.New(httpx.Config{}), nil),
		logger,
	)
	handler := NewHandler(NewService(
		resolver, noPOIs{}, localcontext.StubWeather{}, true,
		localcontext.StubTransportWithDrive{Fallback: localcontext.StubTransport{}},
		localcontext.BookingComDeepLink{}, localcontext.OpenTableDeepLink{},
		freePlan{}, logger,
	))

	origin := "Zzzqqxwvu"
	start := time.Now().AddDate(0, 0, 3)
	_, err := handler.CompareWeekend(ctx, connect.NewRequest(&comparev1.CompareWeekendRequest{
		OriginCity:         &origin,
		CandidateCityNames: []string{"Qqqzzxwvu", "Xxzzqqwvu"},
		StartDate:          timestamppb.New(start),
		EndDate:            timestamppb.New(start.Add(48 * time.Hour)),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
