package service

import (
	"context"
	"log/slog"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

// Close stops background workers owned by the service.
func (l *ServiceImpl) Close() {
	if l.deadLetterCancel != nil {
		l.deadLetterCancel()
	}
}

// processDeadLetterQueue drains any stream events that could not be delivered and logs them.
func (l *ServiceImpl) processDeadLetterQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-l.deadLetterCh:
			l.logger.Warn("stream event routed to dead letter queue", slog.String("event_id", event.EventID), slog.String("type", event.Type), slog.String("error", event.Error))
		}
	}
}

func (l *ServiceImpl) enqueueDeadLetter(ctx context.Context, event locitypes.StreamEvent) {
	select {
	case l.deadLetterCh <- event:
	case <-ctx.Done():
		l.logger.WarnContext(ctx, "could not enqueue stream event after context cancellation", slog.String("eventType", event.Type))
	default:
		l.logger.WarnContext(ctx, "dead letter queue full, dropping stream event", slog.String("eventType", event.Type))
	}
}

func (l *ServiceImpl) sendEvent(ctx context.Context, ch chan<- locitypes.StreamEvent, event locitypes.StreamEvent, retries int) bool {
	for i := 0; i < retries; i++ {
		if event.EventID == "" {
			event.EventID = uuid.New().String()
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now()
		}

		select {
		case <-ctx.Done():
			l.logger.WarnContext(ctx, "Context cancelled, not sending stream event", slog.String("eventType", event.Type))
			l.enqueueDeadLetter(ctx, event)
			return false
		default:
			sent := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						l.logger.WarnContext(ctx, "Recovered from panic sending to channel (likely closed)",
							slog.String("eventType", event.Type),
							slog.Any("panic", r))
					}
				}()

				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()

				select {
				case ch <- event:
					sent = true
				case <-ctx.Done():
					l.logger.WarnContext(ctx, "Context cancelled while trying to send stream event",
						slog.String("eventType", event.Type),
						slog.Any("context_err", ctx.Err()))
					l.enqueueDeadLetter(ctx, event)
				case <-timer.C:
					l.logger.WarnContext(ctx, "Dropped stream event due to slow consumer or blocked channel (timeout)",
						slog.String("eventType", event.Type))
					l.enqueueDeadLetter(ctx, event)
				}
			}()

			if sent {
				return true
			}

			if ctx.Err() != nil {
				return false
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
