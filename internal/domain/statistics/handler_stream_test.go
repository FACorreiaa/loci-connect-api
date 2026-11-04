package statistics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	statisticsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/statistics"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/statistics/statisticsv1connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// countingStats counts queries, each of which takes queryTime and honours its
// context. failFirst makes that many leading queries fail.
type countingStats struct {
	stubStatsService
	queries   atomic.Int64
	queryTime time.Duration
	failFirst int64
}

func (s *countingStats) GetMainPageStatistics(ctx context.Context, _ uuid.UUID) (*locitypes.MainPageStatistics, error) {
	n := s.queries.Add(1)
	select {
	case <-time.After(s.queryTime):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if n <= s.failFirst {
		return nil, errors.New("database unavailable")
	}
	return &locitypes.MainPageStatistics{TotalUsersCount: n}, nil
}

func TestStreamMainStats_ManyStreamsShareOneQueryPerTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &countingStats{queryTime: 50 * time.Millisecond}
		h := NewHandler(svc, newStatsTestLogger())
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		defer cancel()

		const streams = 50
		var events atomic.Int64
		var wg sync.WaitGroup
		for range streams {
			wg.Go(func() {
				_ = h.streamMainStats(ctx, 30, func(mainStatsSnapshot) error {
					events.Add(1)
					return nil
				})
			})
		}
		wg.Wait()

		// Once on open and once per 30s tick, however many are watching.
		assert.LessOrEqual(t, svc.queries.Load(), int64(11))
		assert.GreaterOrEqual(t, events.Load(), int64(streams*10))
	})
}

func TestStreamMainStats_ClientCannotAskForFasterTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHandler(&countingStats{}, newStatsTestLogger())
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		defer cancel()

		var events int
		err := h.streamMainStats(ctx, 1, func(mainStatsSnapshot) error {
			events++
			return nil
		})

		require.NoError(t, err)
		assert.LessOrEqual(t, events, 11, "one on open, then at most one per 30s")
	})
}

func TestStreamMainStats_EndsAtMaxLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHandler(&countingStats{}, newStatsTestLogger())
		// Only so that a stream with no lifetime ends and fails the assertion
		// rather than hanging the test.
		ctx, cancel := context.WithTimeout(t.Context(), 2*streamMaxLifetime)
		defer cancel()

		start := time.Now()
		err := h.streamMainStats(ctx, 30, func(mainStatsSnapshot) error { return nil })

		require.NoError(t, err)
		assert.Equal(t, streamMaxLifetime, time.Since(start))
	})
}

// A stream ends at its lifetime, so a client asking for a long interval would
// otherwise reconnect forever without ever being sent anything.
func TestStreamMainStats_SendsOnOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHandler(&countingStats{}, newStatsTestLogger())
		ctx, cancel := context.WithCancel(t.Context())

		var events atomic.Int64
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = h.streamMainStats(ctx, 3600, func(mainStatsSnapshot) error {
				events.Add(1)
				return nil
			})
		}()
		synctest.Wait()

		assert.Equal(t, int64(1), events.Load())
		cancel()
		<-done
	})
}

func TestStreamMainStats_ReportsWhenTheNumbersWereRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHandler(&countingStats{}, newStatsTestLogger())
		read := time.Now()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := make(chan struct{})
		go func() {
			_ = h.streamMainStats(ctx, 30, func(mainStatsSnapshot) error {
				close(first)
				<-ctx.Done()
				return nil
			})
		}()
		<-first

		// A later stream inside the cache window gets the same numbers, so it
		// must be told how old they are.
		time.Sleep(15 * time.Second)
		late, lateCancel := context.WithCancel(ctx)
		var got time.Time
		_ = h.streamMainStats(late, 30, func(snap mainStatsSnapshot) error {
			got = snap.fetchedAt
			lateCancel()
			return nil
		})
		lateCancel()

		assert.Equal(t, read, got)
	})
}

// A failed read must not be cached: the next tick tries again.
func TestStreamMainStats_RecoversFromFailedQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHandler(&countingStats{failFirst: 1}, newStatsTestLogger())
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()

		var events int
		err := h.streamMainStats(ctx, 30, func(mainStatsSnapshot) error {
			events++
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, events)
	})
}

// Whichever stream starts the shared query may leave while it runs. The others
// are waiting on the same result and must still get it.
func TestStreamMainStats_LeaderLeavingDoesNotFailTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &countingStats{queryTime: time.Second}
		h := NewHandler(svc, newStatsTestLogger())

		leaderCtx, leave := context.WithCancel(t.Context())
		go func() {
			_ = h.streamMainStats(leaderCtx, 30, func(mainStatsSnapshot) error { return nil })
		}()
		synctest.Wait() // leader is inside the query

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var events int
		go func() {
			time.Sleep(100 * time.Millisecond)
			leave()
		}()
		err := h.streamMainStats(ctx, 30, func(mainStatsSnapshot) error {
			events++
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, events)
		assert.Equal(t, int64(1), svc.queries.Load())
	})
}

// The whole path over Connect: an event straight away, then a clean end.
func TestStreamMainPageStatistics_OverTheWire(t *testing.T) {
	h := NewHandler(&countingStats{}, newStatsTestLogger())
	h.streamMaxLifetime = 200 * time.Millisecond

	mux := http.NewServeMux()
	path, handler := statisticsv1connect.NewStatisticsServiceHandler(h)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := statisticsv1connect.NewStatisticsServiceClient(srv.Client(), srv.URL)
	stream, err := client.StreamMainPageStatistics(t.Context(), connect.NewRequest(&statisticsv1.StreamMainPageStatisticsRequest{}))
	require.NoError(t, err)
	defer stream.Close()

	require.True(t, stream.Receive(), "first event: %v", stream.Err())
	assert.Equal(t, int64(1), stream.Msg().GetMainStats().GetTotalUsers())
	require.False(t, stream.Receive())
	require.NoError(t, stream.Err())
}
