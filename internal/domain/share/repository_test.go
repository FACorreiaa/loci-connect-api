//go:build integration

package share

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testsupport.MustPool()
	m.Run()
}

func TestShareRepo_PersistAndIncrement_Integration(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "shares")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(pool, logger)
	ctx := context.Background()

	s := &Share{Code: "abc123code", ContentType: 1, ContentID: "poi-1", Title: "Cool Place", Description: "Nice"}
	require.NoError(t, repo.Create(ctx, s))

	// A fresh repo instance (simulating a server restart) still resolves the code.
	repo2 := NewRepository(pool, logger)
	got, err := repo2.GetByCode(ctx, "abc123code")
	require.NoError(t, err)
	assert.Equal(t, "Cool Place", got.Title)
	assert.Equal(t, int32(0), got.ViewCount)

	bumped, err := repo2.IncrementView(ctx, "abc123code")
	require.NoError(t, err)
	assert.Equal(t, int32(1), bumped.ViewCount)

	_, err = repo2.GetByCode(ctx, "does-not-exist")
	require.ErrorIs(t, err, ErrNotFound)
}
