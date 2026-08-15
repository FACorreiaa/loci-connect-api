package service

import (
	"context"
	"fmt"
	"log/slog"

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
	prefVectors        preference.VectorReader
	// assembler grounds generation in retrieved rows. Optional: when nil the
	// chat path behaves exactly as it did before evidence packets existed.
	assembler *retrieval.Assembler

	// events
	deadLetterCh     chan locitypes.StreamEvent
	deadLetterCancel context.CancelFunc
	intentClassifier IntentClassifier
	llmSem           *concurrency.LLMSemaphore
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
) (*ServiceImpl, error) {
	ctx := context.Background()
	aiClient, err := ai.NewChatClient(ctx, aiCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI chat client: %w", err)
	}

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
