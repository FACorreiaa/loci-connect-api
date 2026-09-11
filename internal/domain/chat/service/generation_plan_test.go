package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// fakeGenerationStore stands in for the durable layer. It counts reads so a
// test can assert that a layer was *not* consulted, which is the whole point
// of a tiered cache.
type fakeGenerationStore struct {
	mu   sync.Mutex
	rows map[string]locitypes.LLMGeneration
	gets []string
	puts []locitypes.LLMGeneration
}

func newFakeGenerationStore() *fakeGenerationStore {
	return &fakeGenerationStore{rows: map[string]locitypes.LLMGeneration{}}
}

func (f *fakeGenerationStore) GetGeneration(_ context.Context, cacheKey string) (*locitypes.LLMGeneration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, cacheKey)
	row, ok := f.rows[cacheKey]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (f *fakeGenerationStore) PutGeneration(_ context.Context, g locitypes.LLMGeneration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, g)
	f.rows[g.CacheKey] = g
	return nil
}

func (f *fakeGenerationStore) PurgeGenerationsByCity(context.Context, string) (int64, error) {
	return 0, nil
}

func (f *fakeGenerationStore) DeleteExpiredGenerations(context.Context) (int64, error) {
	return 0, nil
}

func (f *fakeGenerationStore) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.gets)
}

// getCountFor counts reads of one key, so a test can watch a single part.
func (f *fakeGenerationStore) getCountFor(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, got := range f.gets {
		if got == key {
			n++
		}
	}
	return n
}

func (f *fakeGenerationStore) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

var _ repository.GenerationStore = (*fakeGenerationStore)(nil)

// newPlanService builds a service with a real in-process cache and no
// provider: nothing in the plan/lookup/write path may call one.
func newPlanService(t *testing.T, store repository.GenerationStore) *ServiceImpl {
	t.Helper()
	cache, err := cachestore.New(cachestore.Config{}, slog.Default())
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	return &ServiceImpl{
		logger:      slog.Default(),
		cache:       cache,
		generations: store,
		model:       "test-model",
	}
}

// testChatContext is an itinerary turn: three parts, one traveller.
func testChatContext(t *testing.T, message string) *common.ChatContext {
	t.Helper()
	return &common.ChatContext{
		Ctx:      t.Context(),
		UserID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		CityName: "Funchal",
		Message:  message,
		Domain:   locitypes.DomainItinerary,
	}
}

// validCityData is output the parser accepts, so the write path will store it.
const validCityData = `{"city": "Funchal", "country": "Portugal"}`

// A part already in memory must not reach the database. The point of the
// memory tier is that a warm pod answers without a round trip.
func TestLookupPrefersMemoryOverTheDurableLayer(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	if len(plan) != 3 {
		t.Fatalf("planned %d parts, want 3", len(plan))
	}
	l.cache.Set(plan[0].CacheKey, validCityData, time.Minute)

	if hits := l.lookupGenerations(cc.Ctx, plan); hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
	if plan[0].Hit == nil || plan[0].Hit.Layer != observability.LLMCacheLayerMemory {
		t.Fatalf("city_data hit = %+v, want a memory hit", plan[0].Hit)
	}
	for _, key := range store.gets {
		if key == plan[0].CacheKey {
			t.Error("the durable layer was read for a part already in memory")
		}
	}
}

// A durable hit is mirrored into memory, so the next request on this pod pays
// nothing at all. Without this a popular city would query Postgres on every
// single request for the life of the row.
func TestDurableHitRepopulatesMemory(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	key := plan[0].CacheKey
	store.rows[key] = locitypes.LLMGeneration{
		CacheKey:     key,
		Part:         string(partCityData),
		Response:     validCityData,
		ModelVersion: "test-model-2026-01",
		TokensOut:    420,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	if hits := l.lookupGenerations(cc.Ctx, plan); hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
	hit := plan[0].Hit
	if hit == nil || hit.Layer != observability.LLMCacheLayerDB {
		t.Fatalf("city_data hit = %+v, want a db hit", hit)
	}
	// The saved-token count travels with the row: it is what the analytics
	// event reports this hit was worth.
	if hit.TokensOut != 420 || hit.ModelVersion != "test-model-2026-01" {
		t.Errorf("hit lost the row's provenance: %+v", hit)
	}

	firstRound := store.getCountFor(key)
	second := l.planGeneration(cc)
	if hits := l.lookupGenerations(cc.Ctx, second); hits != 1 {
		t.Fatalf("second lookup hits = %d, want 1", hits)
	}
	if second[0].Hit.Layer != observability.LLMCacheLayerMemory {
		t.Errorf("second lookup served from %q, want memory", second[0].Hit.Layer)
	}
	if store.getCountFor(key) != firstRound {
		t.Error("the durable layer was read again for a part it had just repopulated in memory")
	}
}

// An expired row is not an answer. The store's own query hides expired rows;
// the service repeats the check so a stale row can never be replayed, and so
// the memory TTL derived from it is never negative.
func TestExpiredDurableRowIsAMiss(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	key := plan[0].CacheKey
	store.rows[key] = locitypes.LLMGeneration{
		CacheKey:  key,
		Part:      string(partCityData),
		Response:  validCityData,
		ExpiresAt: time.Now().Add(-time.Minute),
	}

	if hits := l.lookupGenerations(cc.Ctx, plan); hits != 0 {
		t.Fatalf("hits = %d, want 0 — an expired row was served", hits)
	}
	if _, found := l.cache.Get(key); found {
		t.Error("an expired row was mirrored into memory")
	}
}

// The saving that matters: when every part is already cached, retrieval — and
// the embedding call inside it — never runs. A turn that hits on everything
// costs zero provider calls, which is the reason the packet is not in the key.
func TestFullCacheHitSkipsEvidenceRetrieval(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	assembled := 0
	l.assembleEvidence = func(*common.ChatContext) { assembled++ }

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	for _, p := range l.planGeneration(cc) {
		l.cache.Set(p.CacheKey, validCityData, time.Minute)
	}

	plan := l.resolveGenerationPlan(cc)
	if assembled != 0 {
		t.Errorf("evidence was retrieved %d times for a fully cached turn", assembled)
	}
	for _, p := range plan {
		if p.Hit == nil {
			t.Errorf("%s missed on a fully warmed cache", p.Part)
		}
	}
	if cc.CacheKey == "" {
		t.Error("the request key was not set; the start event and the audit row need it")
	}
}

// One missing part is enough to need evidence: the parts that do have to be
// generated must be grounded in real rows.
func TestPartialCacheHitStillRetrievesEvidence(t *testing.T) {
	l := newPlanService(t, newFakeGenerationStore())

	assembled := 0
	l.assembleEvidence = func(*common.ChatContext) { assembled++ }

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	l.cache.Set(plan[0].CacheKey, validCityData, time.Minute)

	l.resolveGenerationPlan(cc)
	if assembled != 1 {
		t.Errorf("evidence retrieved %d times, want 1", assembled)
	}
}

// "Right now" cannot be answered from a cache, so such a turn neither reads
// one nor writes one.
func TestLiveWordsBypassTheCacheEntirely(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "what is open right now in funchal")
	plan := l.resolveGenerationPlan(cc)

	if cc.Cacheable {
		t.Fatal("a request pinned to the present moment was marked cacheable")
	}
	if store.getCount() != 0 {
		t.Errorf("the durable layer was read %d times for an uncacheable turn", store.getCount())
	}
	for _, p := range plan {
		if p.Hit != nil {
			t.Errorf("%s was served from cache on an uncacheable turn", p.Part)
		}
	}

	cc.PartOutcomes = map[string]common.PartOutcome{
		string(partCityData): {ServedFrom: servedFromLLM},
	}
	l.persistGenerations(cc.Ctx, cc, plan, map[string]string{string(partCityData): validCityData}, uuid.Nil)

	if store.putCount() != 0 {
		t.Error("an uncacheable turn was written to the durable layer")
	}
	if _, found := l.cache.Get(plan[0].CacheKey); found {
		t.Error("an uncacheable turn was written to memory")
	}
}

// Output that cannot be parsed is not an answer, and caching it would replay
// the failure for the whole lifetime of the row.
func TestInvalidOutputIsNeverStored(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)

	cc.PartOutcomes = map[string]common.PartOutcome{
		string(partCityData): {ServedFrom: servedFromLLM, ModelID: "test-model", PromptHash: "abc"},
	}
	l.persistGenerations(cc.Ctx, cc, plan[:1],
		map[string]string{string(partCityData): "Sorry, I could not produce JSON."}, uuid.Nil)

	if store.putCount() != 0 {
		t.Errorf("unparseable output was written to the durable layer: %+v", store.puts)
	}
	if _, found := l.cache.Get(plan[0].CacheKey); found {
		t.Error("unparseable output was written to memory")
	}
}

// The control for the test above: valid output is stored in both layers, with
// the provenance the audit trail needs.
func TestValidGeneratedPartIsStoredInBothLayers(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	cc.SessionID = uuid.New()
	plan := l.planGeneration(cc)

	cc.PartOutcomes = map[string]common.PartOutcome{
		string(partCityData): {
			ServedFrom:   servedFromLLM,
			CacheKey:     plan[0].CacheKey,
			ModelID:      "test-model",
			ModelVersion: "test-model-2026-01",
			PromptHash:   "deadbeef",
			TokensIn:     120,
			TokensOut:    900,
		},
	}
	l.persistGenerations(cc.Ctx, cc, plan[:1],
		map[string]string{string(partCityData): validCityData}, uuid.Nil)

	if store.putCount() != 1 {
		t.Fatalf("stored %d rows, want 1", store.putCount())
	}
	row := store.puts[0]
	switch {
	case row.CacheKey != plan[0].CacheKey:
		t.Errorf("row written under %q, want the key it was looked up by", row.CacheKey)
	case row.TemplateVersion != generationTemplateVersion:
		t.Errorf("TemplateVersion = %q", row.TemplateVersion)
	case row.Part != string(partCityData):
		t.Errorf("Part = %q", row.Part)
	case row.ModelID != "test-model" || row.ModelVersion != "test-model-2026-01":
		t.Errorf("row lost the model it was generated on: %+v", row)
	case row.PromptHash != "deadbeef":
		t.Errorf("PromptHash = %q", row.PromptHash)
	case row.City != "funchal":
		t.Errorf("City = %q, want the normalised name", row.City)
	case row.PacketID != "ungrounded":
		t.Errorf("PacketID = %q", row.PacketID)
	case row.TokensIn != 120 || row.TokensOut != 900:
		t.Errorf("row lost the token counts: %+v", row)
	case !row.ExpiresAt.After(time.Now().Add(29 * 24 * time.Hour)):
		t.Errorf("ExpiresAt = %s, want roughly 30 days out for city_data", row.ExpiresAt)
	}

	cached, found := l.cache.Get(plan[0].CacheKey)
	if !found || cached != validCityData {
		t.Errorf("memory holds %v, want the generated text", cached)
	}
}

// A replayed part is not regenerated, so it must not refresh the lifetime of
// the row that produced it.
func TestCachedPartsAreNotWrittenBack(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	cc.PartOutcomes = map[string]common.PartOutcome{
		string(partCityData): {ServedFrom: observability.LLMCacheLayerDB},
	}

	l.persistGenerations(cc.Ctx, cc, plan[:1],
		map[string]string{string(partCityData): validCityData}, uuid.Nil)

	if store.putCount() != 0 {
		t.Error("a part served from cache was written back to the cache")
	}
}

// The audit row used to record a static model name and leave every cache and
// token column null. It has to say what actually happened: which model, which
// layers, how many tokens, and one row-level verdict on whether anything was
// generated at all.
func TestInteractionRowRecordsWhatTheTurnCost(t *testing.T) {
	l := newPlanService(t, newFakeGenerationStore())
	l.provider = "configured-provider"
	// The key, and so the row, names the model the request was planned on.
	l.model = "deepseek-v4-flash"

	cc := testChatContext(t, "three days in funchal")
	cc.SessionID = uuid.New()
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	cc.CacheKey = requestCacheKey(plan)

	cc.PartOutcomes = map[string]common.PartOutcome{
		string(partCityData): {
			ServedFrom: observability.LLMCacheLayerMemory,
			CacheKey:   plan[0].CacheKey,
			ModelID:    "deepseek-v4-flash",
			TokensOut:  300,
		},
		string(partGeneralPOIs): {
			ServedFrom: observability.LLMCacheLayerDB,
			CacheKey:   plan[1].CacheKey,
			ModelID:    "deepseek-v4-flash",
			TokensOut:  200,
		},
		string(partItinerary): {
			ServedFrom:   servedFromLLM,
			CacheKey:     plan[2].CacheKey,
			ModelID:      "deepseek-v4-flash",
			ModelVersion: "deepseek-v4-flash-2026-05",
			PromptHash:   "f00d",
			TokensIn:     1200,
			TokensOut:    5000,
		},
	}

	row := l.buildInteractionRow(cc, plan, "response text", time.Now().Add(-2*time.Second))

	switch {
	case row.ModelUsed != "deepseek-v4-flash":
		t.Errorf("ModelUsed = %q, want the model the request was planned on", row.ModelUsed)
	case row.Provider != "deepseek":
		t.Errorf("Provider = %q, want the vendor the model id names", row.Provider)
	case row.CacheKey != cc.CacheKey:
		t.Errorf("CacheKey = %q", row.CacheKey)
	case row.CacheHit:
		t.Error("CacheHit is true although the itinerary was generated")
	case row.PromptHash == "":
		t.Error("PromptHash is empty")
	case row.PromptTokens != 1200 || row.CompletionTokens != 5500 || row.TotalTokens != 6700:
		t.Errorf("tokens = %d/%d/%d, want 1200/5500/6700",
			row.PromptTokens, row.CompletionTokens, row.TotalTokens)
	case !row.IsStreaming:
		t.Error("IsStreaming is false on the streaming path")
	case row.LatencyMs < 1000:
		t.Errorf("LatencyMs = %d, want the elapsed time", row.LatencyMs)
	}

	var payload struct {
		Parts map[string]struct {
			ServedFrom   string `json:"served_from"`
			CacheKey     string `json:"cache_key"`
			Model        string `json:"model"`
			ModelVersion string `json:"model_version"`
			TokensOut    int    `json:"tokens_out"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(row.ResponsePayload, &payload); err != nil {
		t.Fatalf("ResponsePayload: %v", err)
	}
	if len(payload.Parts) != 3 {
		t.Fatalf("payload holds %d parts, want 3", len(payload.Parts))
	}
	if got := payload.Parts[string(partCityData)].ServedFrom; got != observability.LLMCacheLayerMemory {
		t.Errorf("city_data served_from = %q", got)
	}
	if got := payload.Parts[string(partItinerary)].ModelVersion; got != "deepseek-v4-flash-2026-05" {
		t.Errorf("itinerary model_version = %q", got)
	}
}

// CacheHit is a claim about the whole turn: it is true only when nothing was
// generated, because that is the only case where the provider was not paid.
func TestInteractionRowFlagsAFullCacheHit(t *testing.T) {
	l := newPlanService(t, newFakeGenerationStore())

	cc := testChatContext(t, "three days in funchal")
	cc.Cacheable = true
	plan := l.planGeneration(cc)
	cc.PartOutcomes = map[string]common.PartOutcome{}
	for _, p := range plan {
		cc.PartOutcomes[string(p.Part)] = common.PartOutcome{
			ServedFrom: observability.LLMCacheLayerMemory,
			CacheKey:   p.CacheKey,
			ModelID:    "test-model",
		}
	}

	row := l.buildInteractionRow(cc, plan, "response text", time.Now())
	if !row.CacheHit {
		t.Error("CacheHit is false although every part was replayed")
	}
	if row.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 — nothing was generated", row.TotalTokens)
	}
}
