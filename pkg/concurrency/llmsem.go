package concurrency

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// LLMSemaphore limits concurrent outbound Gemini calls across chat and POI services.
type LLMSemaphore struct {
	sem *semaphore.Weighted
}

// NewLLMSemaphore returns a semaphore that allows at most maxConcurrent simultaneous
// LLM calls. When maxConcurrent <= 0, returns nil (no limit).
func NewLLMSemaphore(maxConcurrent int) *LLMSemaphore {
	if maxConcurrent <= 0 {
		return nil
	}
	return &LLMSemaphore{sem: semaphore.NewWeighted(int64(maxConcurrent))}
}

// Acquire blocks until a slot is available or ctx is canceled.
// The returned release function must be called when the LLM call completes.
func (s *LLMSemaphore) Acquire(ctx context.Context) (release func(), err error) {
	if s == nil || s.sem == nil {
		return func() {}, nil
	}
	if err := s.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { s.sem.Release(1) }, nil
}
