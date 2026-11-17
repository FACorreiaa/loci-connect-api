package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
)

func TestLedger_SharesTheAssistantsCeiling(t *testing.T) {
	store := newMemStore()
	l := NewLedger(store, 25_000, nil)
	l.now = func() time.Time { return time.Date(2026, 11, 17, 23, 30, 0, 0, time.UTC) }
	ctx := context.Background()
	require.True(t, l.CanSpend(ctx))

	// A report that searched twice: 2000 in, 600 out, two searches.
	l.Spent(ctx, uuid.New(), "claude-haiku-5-5", anthropic.ToolUsage{InputTokens: 2000, OutputTokens: 600, WebSearchRequests: 2})
	require.Equal(t, int64(200+300+20_000), store.spend.CostMicroUSD, "tokens at list price, searches at a cent each")
	require.True(t, l.CanSpend(ctx))

	l.Spent(ctx, uuid.New(), "claude-haiku-5-5", anthropic.ToolUsage{WebSearchRequests: 1})
	require.False(t, l.CanSpend(ctx), "past the ceiling: the next report is written without the model")

	l.Spent(ctx, uuid.New(), "x", anthropic.ToolUsage{})
	require.Equal(t, int64(30_500), store.spend.CostMicroUSD, "a call that cost nothing records nothing")
}
