package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/profiles"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tags"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/analytics"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func (l *ServiceImpl) acquireLLMSlot(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	return l.llmSem.Acquire(ctx)
}

const (
	defaultTemperature = 0.5
)

type ChatSession struct {
	History []genai.Chat
}

// Mutex for thread-safe access

// Ensure implementation satisfies the interface
var _ LlmInteractiontService = (*ServiceImpl)(nil)

// LlmInteractiontService defines the business logic contract for user operations.
type LlmInteractiontService interface {
	StartChat(ctx context.Context, userID, profileID uuid.UUID, cityName, message string, userLocation *locitypes.UserLocation) (*locitypes.ChatResponse, error)
	ContinueChat(ctx context.Context, userID, sessionID uuid.UUID, message, cityName string) (*locitypes.ChatResponse, error)
	SaveItineraryFromInteraction(ctx context.Context, userID uuid.UUID, req locitypes.BookmarkRequest) (uuid.UUID, error)
	GetBookmarkedItineraries(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.PaginatedUserItinerariesResponse, error)
	GetBookmarkedPOIs(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.PaginatedUserPOIsResponse, error)
	BookmarkPOI(ctx context.Context, userID uuid.UUID, req locitypes.BookmarkRequest) (uuid.UUID, error)
	RemoveItinerary(ctx context.Context, userID, itineraryID uuid.UUID) error
	RemovePOI(ctx context.Context, userID, poiID uuid.UUID) error
	GetPOIDetailedInfosResponse(ctx context.Context, userID uuid.UUID, city string, lat, lon float64) (*locitypes.POIDetailedInfo, error)

	ContinueSessionStreamed(
		ctx context.Context,
		sessionID uuid.UUID,
		message string,
		userLocation *locitypes.UserLocation, // For distance sorting context
		eventCh chan<- locitypes.StreamEvent, // Channel to send events back
	) error

	ProcessUnifiedChatMessageStream(cc common.ChatContext) error

	// GetUserChatSessions Chat session management
	GetUserChatSessions(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.ChatSessionsResponse, error)
	GetChatSession(ctx context.Context, userID, sessionID uuid.UUID) (*locitypes.ChatSession, error)
	EndSession(ctx context.Context, userID, sessionID uuid.UUID) error
	GetRecentInteractions(ctx context.Context, userID uuid.UUID, pagination *commonpb.PaginationRequest) (*chatv1.GetRecentInteractionsResponse, error)
}

type IntentClassifier interface {
	Classify(ctx context.Context, message string) (locitypes.IntentType, error) // e.g., "start_trip", "modify_itinerary"
}

// ServiceImpl provides the implementation for LlmInteractiontService.
//
//revive:disable-next-line:exported
type ServiceImpl struct {
	logger             *slog.Logger
	interestRepo       interests.Repository
	searchProfileRepo  profiles.Repository
	searchProfileSvc   profiles.Service // Add service for enhanced methods
	tagsRepo           tags.Repository
	aiClient           generativeAI.ChatClient
	embeddingService   generativeAI.EmbeddingClient
	llmInteractionRepo repository.Repository
	cityRepo           city.Repository
	poiRepo            poi.Repository
	poiSvc             poi.Service // POI service for nearby queries with cache + DB + LLM fallback
	listSvc            itinerarylist.Service
	tripRepo           trip.Repository // auto-persist generated itineraries as editable trips
	cache              cachestore.Store
	model              string
	// provider is the configured upstream, used to attribute an interaction
	// whose model id does not name its vendor.
	provider    string
	prefVectors preference.VectorReader
	// assembler grounds generation in retrieved rows. Optional: when nil the
	// chat path behaves exactly as it did before evidence packets existed.
	assembler *retrieval.Assembler
	// assembleEvidence overrides how a turn retrieves its evidence. Nil in
	// production; tests set it to observe that a fully cached turn retrieves
	// nothing at all.
	assembleEvidence func(cc *common.ChatContext)

	// generations is the durable layer of the generation cache. Optional: nil
	// means the in-process store is the only layer, which is the state under
	// the CACHE_GENERATIONS_ENABLED kill-switch and in every unit test.
	generations repository.GenerationStore

	// analytics records product events server-side. Nil records nothing, which
	// is the normal state wherever no PostHog key is configured.
	analytics *analytics.Recorder

	// events
	deadLetterCh     chan locitypes.StreamEvent
	deadLetterCancel context.CancelFunc
	intentClassifier IntentClassifier
	llmSem           *concurrency.LLMSemaphore
}

// Option adjusts how the service reaches its model provider.
type Option func(generativeAI.ChatClient) generativeAI.ChatClient

// WithClientWrapper wraps the provider chain this service was built with.
//
// It exists so a request can be served by the caller's own provider without
// this package knowing that per-user credentials exist: the wrapper decides,
// and the dozen call sites that reach for the client are unchanged. See
// aicreds.Router.
func WithClientWrapper(wrap func(generativeAI.ChatClient) generativeAI.ChatClient) Option {
	return Option(wrap)
}

// modelResolver is what a client wrapper that routes per request exposes, so
// the service can learn which model a given context will be answered by
// without importing the router. aicreds.Router satisfies it.
type modelResolver interface {
	ModelFor(ctx context.Context) string
}

// modelFor names the model this request is planned to run on. It asks the
// client when the client routes per request, falls back to the client's
// process-wide model, and finally to the configured one, so the answer is
// never empty for a service that has a model at all.
//
// It is the planned model, not necessarily the one that answers: a chain may
// fail over mid-request. Cache keys use this value; the answered model is
// recorded from the stream (streamResult.ModelVersion).
func (l *ServiceImpl) modelFor(ctx context.Context) string {
	if l == nil {
		return ""
	}
	if r, ok := l.aiClient.(modelResolver); ok {
		if m := r.ModelFor(ctx); m != "" {
			return m
		}
	}
	if l.aiClient != nil {
		if m := l.aiClient.Model(); m != "" {
			return m
		}
	}
	return l.model
}

func applyOptions(client generativeAI.ChatClient, opts []Option) generativeAI.ChatClient {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if wrapped := opt(client); wrapped != nil {
			client = wrapped
		}
	}
	return client
}

// NewLlmInteractiontService creates a new user service instance.
func NewLlmInteractiontService(interestRepo interests.Repository,
	searchProfileRepo profiles.Repository,
	searchProfileSvc profiles.Service,
	tagsRepo tags.Repository,
	llmInteractionRepo repository.Repository,
	cityRepo city.Repository,
	poiRepo poi.Repository,
	poiSvc poi.Service,
	listSvc itinerarylist.Service,
	tripRepo trip.Repository,
	logger *slog.Logger,
	aiCfg config.AIConfig,
	llmSem *concurrency.LLMSemaphore,
	appCache cachestore.Store,
	opts ...Option,
) (*ServiceImpl, error) {
	ctx := context.Background()
	aiClient, err := ai.NewChatClient(ctx, aiCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI chat client: %w", err)
	}
	aiClient = applyOptions(aiClient, opts)

	// Initialize embedding service
	embeddingService, err := ai.NewEmbeddingClient(ctx, aiCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding service: %w", err)
	}

	deadLetterCtx, deadLetterCancel := context.WithCancel(context.Background())

	// Initialize RAG service
	service := &ServiceImpl{
		logger:             logger,
		tagsRepo:           tagsRepo,
		interestRepo:       interestRepo,
		searchProfileRepo:  searchProfileRepo,
		searchProfileSvc:   searchProfileSvc,
		aiClient:           aiClient,
		embeddingService:   embeddingService,
		llmInteractionRepo: llmInteractionRepo,
		cityRepo:           cityRepo,
		poiRepo:            poiRepo,
		poiSvc:             poiSvc,
		listSvc:            listSvc,
		tripRepo:           tripRepo,
		cache:              appCache,
		model:              aiCfg.Model,
		provider:           aiCfg.Provider,
		deadLetterCh:       make(chan locitypes.StreamEvent, 100),
		deadLetterCancel:   deadLetterCancel,
		intentClassifier:   &locitypes.SimpleIntentClassifier{},
		llmSem:             llmSem,
	}
	go service.processDeadLetterQueue(deadLetterCtx)
	return service, nil
}

// SetPreferenceVectors enables preference-aware semantic POI ranking in chat.
func (l *ServiceImpl) SetPreferenceVectors(r preference.VectorReader) {
	if l != nil {
		l.prefVectors = r
	}
}

// SetRetrievalAssembler enables grounded generation: retrieved POI rows are
// rendered into the prompt and the answer is checked against them afterwards.
// Without it the service keeps its previous behaviour of generating from the
// city name and preference text alone.
func (l *ServiceImpl) SetRetrievalAssembler(a *retrieval.Assembler) {
	if l != nil {
		l.assembler = a
	}
}

// SetAnalytics attaches the product-event recorder, so every part of every
// answer reports which cache layer served it and what that saved.
func (l *ServiceImpl) SetAnalytics(r *analytics.Recorder) {
	if l != nil {
		l.analytics = r
	}
}

// expiredGenerationSweep is how often expired rows are swept out of
// llm_generations. Expired rows are already invisible to reads (the lookup
// filters on expires_at), so this is housekeeping for the table's size, not
// for correctness — daily is plenty.
const expiredGenerationSweep = 24 * time.Hour

// SetGenerationStore turns on the durable layer of the generation cache and
// starts the sweep that keeps its table from growing forever.
//
// Without it the service still caches in memory, which is what every unit test
// and any deployment with CACHE_GENERATIONS_ENABLED=false does.
func (l *ServiceImpl) SetGenerationStore(s repository.GenerationStore) {
	if l == nil || s == nil {
		return
	}
	l.generations = s

	logger := l.logger
	concurrency.Run(logger, func() {
		ticker := time.NewTicker(expiredGenerationSweep)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			deleted, err := s.DeleteExpiredGenerations(ctx)
			cancel()
			if err != nil {
				logger.Warn("failed to sweep expired generations", "error", err)
				continue
			}
			logger.Info("swept expired generations", "deleted", deleted)
		}
	})
}
