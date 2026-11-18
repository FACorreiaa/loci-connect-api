package assistant

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
)

// Ledger lets another feature that calls Anthropic directly spend from the
// same daily ceiling as the assistant, and stop at it. The ceiling exists
// because the key's budget is shared: a feature with a ledger of its own
// would let the two together overrun it.
type Ledger struct {
	store   Store
	ceiling int64
	logger  *slog.Logger
	now     func() time.Time
}

// NewLedger builds a ledger over the assistant's store. A ceiling of zero
// gets the assistant's default.
func NewLedger(store Store, ceilingMicroUSD int64, logger *slog.Logger) *Ledger {
	if ceilingMicroUSD == 0 {
		ceilingMicroUSD = defaultDailyCeilingMicroUSD
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Ledger{store: store, ceiling: ceilingMicroUSD, logger: logger, now: time.Now}
}

func (l *Ledger) day() time.Time { return l.now().UTC().Truncate(24 * time.Hour) }

// CanSpend is false once the day's ceiling is reached. A failure reading it
// lets the call through, as the assistant does: the provider's own cap
// still stands.
func (l *Ledger) CanSpend(ctx context.Context) bool {
	spent, err := l.store.DaySpendMicroUSD(ctx, l.day())
	if err != nil {
		l.logger.Warn("ledger: day spend not read", slog.Any("error", err))
		return true
	}
	return spent < l.ceiling
}

// Spent records what one model call cost against the caller and the day.
func (l *Ledger) Spent(ctx context.Context, userID uuid.UUID, _ string, u anthropic.ToolUsage) {
	var sp Spend
	sp.add(u)
	if sp == (Spend{}) {
		return
	}
	// Its own context: a traveller who hung up still spent it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := l.store.AddSpend(ctx, userID, l.day(), sp); err != nil {
		l.logger.Warn("ledger: spend not recorded", slog.Any("error", err))
	}
}
