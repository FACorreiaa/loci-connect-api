package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type stubEmbedRepo struct {
	missing  []uuid.UUID
	missErr  error
	pois     map[uuid.UUID]*locitypes.POIDetailedInfo
	stored   map[uuid.UUID][]float32
	storeErr error
	asked    []uuid.UUID
}

func (s *stubEmbedRepo) POIIDsMissingEmbeddings(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	s.asked = append(s.asked, ids...)
	return s.missing, s.missErr
}

func (s *stubEmbedRepo) GetPOIByID(_ context.Context, id uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	return s.pois[id], nil
}

func (s *stubEmbedRepo) UpdatePOIEmbedding(_ context.Context, id uuid.UUID, embedding []float32) error {
	if s.storeErr != nil {
		return s.storeErr
	}
	if s.stored == nil {
		s.stored = map[uuid.UUID][]float32{}
	}
	s.stored[id] = embedding
	return nil
}

type stubEmbedder struct {
	calls int
	err   error
}

func (s *stubEmbedder) GeneratePOIEmbedding(_ context.Context, _, _, _ string) ([]float32, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

// The point of the whole file: a place discovered this turn is searchable on
// the next one, instead of waiting for the 03:15 job.
func TestNewPOIsAreEmbeddedAtIngest(t *testing.T) {
	fresh := uuid.New()
	repo := &stubEmbedRepo{
		missing: []uuid.UUID{fresh},
		pois:    map[uuid.UUID]*locitypes.POIDetailedInfo{fresh: {ID: fresh, Name: "Pico do Arieiro", Category: "viewpoint"}},
	}
	client := &stubEmbedder{}

	embedPOIsWithDependencies(context.Background(), []uuid.UUID{fresh}, repo, client, nil)

	if len(repo.stored[fresh]) != 3 {
		t.Errorf("the new place was not embedded: %+v", repo.stored)
	}
	if client.calls != 1 {
		t.Errorf("expected one provider call, got %d", client.calls)
	}
}

// A turn mostly recommends places we already know. Re-embedding them would
// spend a provider call writing the vector that is already there.
func TestPlacesThatAlreadyHaveVectorsAreNotReEmbedded(t *testing.T) {
	known := uuid.New()
	repo := &stubEmbedRepo{missing: nil} // the database says nothing is missing
	client := &stubEmbedder{}

	embedPOIsWithDependencies(context.Background(), []uuid.UUID{known}, repo, client, nil)

	if client.calls != 0 {
		t.Errorf("a known place should cost no provider call, got %d", client.calls)
	}
	if len(repo.asked) != 1 || repo.asked[0] != known {
		t.Errorf("the database should be asked which ids need vectors: %v", repo.asked)
	}
}

// An embedding improves the next turn; it is never a condition of this one.
func TestAFailingProviderIsSwallowed(t *testing.T) {
	fresh := uuid.New()
	repo := &stubEmbedRepo{
		missing: []uuid.UUID{fresh},
		pois:    map[uuid.UUID]*locitypes.POIDetailedInfo{fresh: {ID: fresh, Name: "Somewhere"}},
	}
	client := &stubEmbedder{err: errors.New("provider is unwell")}

	embedPOIsWithDependencies(context.Background(), []uuid.UUID{fresh}, repo, client, nil)

	if len(repo.stored) != 0 {
		t.Error("nothing should be stored when the provider fails")
	}
}

// The cap is a guard against a pathological answer, not a throughput knob:
// what it skips is exactly what the nightly backfill exists to catch.
func TestOnlyTheCapIsEmbeddedInOneTurn(t *testing.T) {
	var ids []uuid.UUID
	pois := map[uuid.UUID]*locitypes.POIDetailedInfo{}
	for i := 0; i < maxIngestEmbeddings+5; i++ {
		id := uuid.New()
		ids = append(ids, id)
		pois[id] = &locitypes.POIDetailedInfo{ID: id, Name: "Place"}
	}
	repo := &stubEmbedRepo{missing: ids, pois: pois}
	client := &stubEmbedder{}

	embedPOIsWithDependencies(context.Background(), ids, repo, client, nil)

	if client.calls != maxIngestEmbeddings {
		t.Errorf("expected the cap (%d) provider calls, got %d", maxIngestEmbeddings, client.calls)
	}
}
