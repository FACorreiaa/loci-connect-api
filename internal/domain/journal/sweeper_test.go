package journal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/storage"
)

type recordingSigner struct {
	mu      sync.Mutex
	deleted []string
	fail    map[string]bool
}

func (r *recordingSigner) PutURL(context.Context, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}

func (r *recordingSigner) GetURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (r *recordingSigner) Head(context.Context, string) (storage.Object, error) {
	return storage.Object{}, nil
}

func (r *recordingSigner) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail[key] {
		return errors.New("minio down")
	}
	r.deleted = append(r.deleted, key)
	return nil
}

type tombRepo struct {
	Repository
	cutoff  time.Time
	keys    []string
	cleared []string
}

func (t *tombRepo) SweepPending(_ context.Context, cutoff time.Time) (int64, error) {
	t.cutoff = cutoff
	return 0, nil
}

func (t *tombRepo) Tombstones(context.Context, int) ([]string, error) { return t.keys, nil }

func (t *tombRepo) ClearTombstones(_ context.Context, keys []string) error {
	t.cleared = append(t.cleared, keys...)
	return nil
}

// A key whose delete failed stays tombstoned for the next sweep.
func TestSweep_KeepsTombstoneWhenDeleteFails(t *testing.T) {
	repo := &tombRepo{keys: []string{"a", "b"}}
	signer := &recordingSigner{fail: map[string]bool{"b": true}}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	require.NoError(t, NewSweeper(repo, signer, nil, nil).Sweep(context.Background(), now))
	require.Equal(t, now.Add(-pendingTTL), repo.cutoff)
	require.Equal(t, []string{"a"}, repo.cleared)
}

// With storage off the pending rows still go; tombstones wait for storage.
func TestSweep_WithoutStorageKeepsTombstones(t *testing.T) {
	repo := &tombRepo{keys: []string{"a"}}
	require.NoError(t, NewSweeper(repo, nil, nil, nil).Sweep(context.Background(), time.Now()))
	require.Empty(t, repo.cleared)
}
