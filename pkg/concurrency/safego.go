// Package concurrency provides goroutine helpers that contain panics so they
// cannot leak workers tracked by a sync.WaitGroup.
package concurrency

import (
	"log/slog"
	"sync"
)

// Go runs f in a new goroutine that decrements wg on exit and recovers any
// panic, logging it via log instead of crashing the process.
func Go(wg *sync.WaitGroup, log *slog.Logger, f func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				if log != nil {
					log.Error("worker goroutine panicked", "recover", r)
				}
			}
		}()
		f()
	}()
}
