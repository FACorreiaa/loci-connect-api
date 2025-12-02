package repository

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	a "github.com/petar-dambovaliev/aho-corasick"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

type (
	LlmInteraction                   = types.LlmInteraction
	AIItineraryResponse              = types.AIItineraryResponse
	GeneralCityData                  = types.GeneralCityData
	AiCityResponse                   = types.AiCityResponse
	GenAIResponse                    = types.GenAIResponse
	AIRequestPayloadForLog           = types.AIRequestPayloadForLog
	ChatTurn                         = types.ChatTurn
	UserLocation                     = types.UserLocation
	UserSavedItinerary               = types.UserSavedItinerary
	UpdateItineraryRequest           = types.UpdateItineraryRequest
	PaginatedUserItinerariesResponse = types.PaginatedUserItinerariesResponse
	BookmarkRequest                  = types.BookmarkRequest
	ChatMessage                      = types.ChatMessage
	POIDetailrequest                 = types.POIDetailrequest
	POIFilter                        = types.POIFilter
	GeoPoint                         = types.GeoPoint
	SearchPOIRequest                 = types.SearchPOIRequest
	HotelUserPreferences             = types.HotelUserPreferences
	HotelDetailedInfo                = types.HotelDetailedInfo
	HotelPreferenceRequest           = types.HotelPreferenceRequest
	RestaurantUserPreferences        = types.RestaurantUserPreferences
	RestaurantDetailedInfo           = types.RestaurantDetailedInfo
	ChatContextType                  = types.ChatContextType
	DomainType                       = types.DomainType
	DomainDetector                   = types.DomainDetector
	UserPreferenceProfileResponse    = types.UserPreferenceProfileResponse
)

const (
	ContextHotels      ChatContextType = types.ContextHotels
	ContextRestaurants ChatContextType = types.ContextRestaurants
	ContextItineraries ChatContextType = types.ContextItineraries
	ContextGeneral     ChatContextType = types.ContextGeneral
)

type StartChatRequest struct {
	CityName       string          `json:"city_name"`
	ContextType    ChatContextType `json:"context_type"`
	InitialMessage string          `json:"initial_message,omitempty"`
}

type ContinueChatRequest struct {
	Message     string          `json:"message"`
	CityName    string          `json:"city_name,omitempty"`
	ContextType ChatContextType `json:"context_type"`
}

//

// Aho-Corasick matchers for intent classification
var (
	intentAddBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	intentAddMatcher = intentAddBuilder.Build([]string{"add", "include", "visit"})

	intentRemoveBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	intentRemoveMatcher = intentRemoveBuilder.Build([]string{"remove", "delete", "skip"})

	intentQuestionBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	intentQuestionMatcher = intentQuestionBuilder.Build([]string{"what", "where", "how", "why", "when"})
)

type SimpleIntentClassifier struct{}

func (c *SimpleIntentClassifier) Classify(_ context.Context, message string) (IntentType, error) {
	message = strings.ToLower(message)

	// Check for add/include/visit intent
	iter := intentAddMatcher.Iter(message)
	if iter.Next() != nil {
		return IntentAddPOI, nil
	}

	// Check for remove/delete/skip intent
	iter = intentRemoveMatcher.Iter(message)
	if iter.Next() != nil {
		return IntentRemovePOI, nil
	}

	// Check for question intent
	iter = intentQuestionMatcher.Iter(message)
	if iter.Next() != nil {
		return IntentAskQuestion, nil
	}

	return IntentModifyItinerary, nil // Default intent
}

// RecentInteraction represents a recent user interaction with cities and POIs
type RecentInteraction struct {
	ID           uuid.UUID                `json:"id"`
	UserID       uuid.UUID                `json:"user_id"`
	CityName     string                   `json:"city_name"`
	CityID       *uuid.UUID               `json:"city_id,omitempty"`
	Prompt       string                   `json:"prompt"`
	ResponseText string                   `json:"response,omitempty"`
	ModelUsed    string                   `json:"model_name"`
	LatencyMs    int                      `json:"latency_ms"`
	CreatedAt    time.Time                `json:"created_at"`
	POIs         []types.POIDetailedInfo  `json:"pois,omitempty"`
	Hotels       []HotelDetailedInfo      `json:"hotels,omitempty"`
	Restaurants  []RestaurantDetailedInfo `json:"restaurants,omitempty"`
}

// RecentInteractionsResponse groups interactions by city
type RecentInteractionsResponse struct {
	Cities  []CityInteractions `json:"cities"`
	Total   int                `json:"total"`
	Page    int                `json:"page"`
	Limit   int                `json:"limit"`
	HasMore bool               `json:"has_more"`
}

// CityInteractions groups interactions for a specific city
type CityInteractions struct {
	CityName         string              `json:"city_name"`
	SessionID        uuid.UUID           `json:"session_id"`
	Interactions     []RecentInteraction `json:"interactions"`
	POICount         int                 `json:"poi_count"`
	LastActivity     time.Time           `json:"last_activity"`
	SessionIDs       []uuid.UUID         `json:"session_ids"` // Changed from SessionID
	Title            string              `json:"title"`
	TotalFavorites   *int                `json:"total_favorites,omitempty"`
	TotalItineraries *int                `json:"total_itineraries,omitempty"`
}

// RecentInteractionsFilter defines filters for recent interactions
type RecentInteractionsFilter struct {
	SortBy          string `json:"sort_by"`          // last_activity, city_name, interaction_count, poi_count
	SortOrder       string `json:"sort_order"`       // asc, desc
	Search          string `json:"search"`           // Search term for city name
	MinInteractions int    `json:"min_interactions"` // Minimum number of interactions
	MaxInteractions int    `json:"max_interactions"` // Maximum number of interactions
}

const (
	SendEventTimeout     = 2 * time.Second
	ContinueEventTimeout = 3 * time.Second
)

type ChatSession struct {
	ID                  uuid.UUID             `json:"id"`
	UserID              uuid.UUID             `json:"user_id"`
	ProfileID           uuid.UUID             `json:"profile_id"`
	CityName            string                `json:"city_name"`
	CurrentItinerary    *AiCityResponse       `json:"current_itinerary,omitempty"`
	ConversationHistory []ConversationMessage `json:"conversation_history"`
	SessionContext      SessionContext        `json:"session_context"` // cigty_name: Barcelona
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	ExpiresAt           time.Time             `json:"expires_at"`
	Status              SessionStatus         `json:"status"` // "active", "expired", etc.

	// Enriched fields for better chat history display
	PerformanceMetrics SessionPerformanceMetrics `json:"performance_metrics"`
	ContentMetrics     SessionContentMetrics     `json:"content_metrics"`
	EngagementMetrics  SessionEngagementMetrics  `json:"engagement_metrics"`
}

type ConversationMessage struct {
	ID          uuid.UUID       `json:"id"`
	Role        MessageRole     `json:"role"` // user, assistant, system
	Content     string          `json:"content"`
	MessageType MessageType     `json:"message_type"` // initial_request, modification_request, response
	Timestamp   time.Time       `json:"timestamp"`
	Metadata    MessageMetadata `json:"metadata,omitempty"`
}

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

type MessageType string

const (
	TypeInitialRequest      MessageType = "initial_request"
	TypeModificationRequest MessageType = "modification_request"
	TypeResponse            MessageType = "response"
	TypeClarification       MessageType = "clarification"
	TypeItineraryResponse               = "itinerary_response"
	TypeError               MessageType = "error" // For errors or unhandled cases
)

type MessageMetadata struct {
	LlmInteractionID *uuid.UUID `json:"llm_interaction_id,omitempty"`
	ModifiedPOICount int        `json:"modified_poi_count,omitempty"`
	RequestType      string     `json:"request_type,omitempty"` // add_poi, remove_poi, modify_preferences, etc.
}

type SessionContext struct {
	CityName            string                               `json:"city_name"` // e.g., "Barcelona"
	LastCityID          uuid.UUID                            `json:"last_city_id"`
	UserPreferences     *types.UserPreferenceProfileResponse `json:"user_preferences"`
	ActiveInterests     []string                             `json:"active_interests"`
	ActiveTags          []string                             `json:"active_tags"`
	ConversationSummary string                               `json:"conversation_summary"`
	ModificationHistory []ModificationRecord                 `json:"modification_history"`
}

type ModificationRecord struct {
	Type        string    `json:"type"` // add_poi, remove_poi, change_preferences
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`
	Applied     bool      `json:"applied"`
}

type SessionStatus string

const (
	StatusActive  SessionStatus = "active"
	StatusExpired SessionStatus = "expired"
	StatusClosed  SessionStatus = "closed"
)

// Request/Response types for chat API
type ChatRequest struct {
	SessionID *uuid.UUID `json:"session_id,omitempty"` // nil for new session
	Message   string     `json:"message"`
	CityName  string     `json:"city_name,omitempty"` // required for new session
}

type ChatResponse struct {
	SessionID             uuid.UUID       `json:"session_id"`
	Message               string          `json:"message"`
	UpdatedItinerary      *AiCityResponse `json:"updated_itinerary,omitempty"`
	IsNewSession          bool            `json:"is_new_session"`
	RequiresClarification bool            `json:"requires_clarification"`
	SuggestedActions      []string        `json:"suggested_actions,omitempty"`
}

// Session Repository Interface
type ChatSessionRepository interface {
	CreateSession(ctx context.Context, session ChatSession) error
	GetSession(ctx context.Context, sessionID uuid.UUID) (*ChatSession, error)
	UpdateSession(ctx context.Context, session ChatSession) error
	AddMessageToSession(ctx context.Context, sessionID uuid.UUID, message ConversationMessage) error
	GetActiveSessionsForUser(ctx context.Context, userID uuid.UUID) ([]ChatSession, error)
	ExpireSession(ctx context.Context, sessionID uuid.UUID) error
	CleanupExpiredSessions(ctx context.Context) error
}

// Chat Service Interface
type ChatService interface {
	StartNewSession(ctx context.Context, userID, profileID uuid.UUID, cityName, initialMessage string) (*ChatResponse, error)
	ContinueSession(ctx context.Context, sessionID uuid.UUID, message string) (*ChatResponse, error)
	GetSessionHistory(ctx context.Context, sessionID uuid.UUID) (*ChatSession, error)
	EndSession(ctx context.Context, sessionID uuid.UUID) error
}

type StreamingChatEvent struct {
	Timestamp        time.Time               `json:"timestamp"`  // Time of the event
	EventID          string                  `json:"event_id"`   // Unique identifier for the event
	EventType        string                  `json:"event_type"` // e.g., "session_started", "city_info", "general_pois", "personalized_poi_chunk", "final_itinerary", "error"
	SessionID        uuid.UUID               `json:"session_id,omitempty"`
	Message          string                  `json:"message,omitempty"` // For general messages or errors
	CityData         *GeneralCityData        `json:"city_data,omitempty"`
	GeneralPOIs      []types.POIDetailedInfo `json:"general_pois,omitempty"`
	PersonalizedPOIs []types.POIDetailedInfo `json:"personalized_pois,omitempty"` // Could send chunks or final list
	Itinerary        *AiCityResponse         `json:"itinerary,omitempty"`         // Could be a partial or final one
	Error            string                  `json:"error_message,omitempty"`
	IsFinal          bool                    `json:"is_final,omitempty"` // Indicates the end of a sequence or the whole stream
	// Add any other relevant data for different event types
}

type Intent struct {
	Type           IntentType             `json:"type"`
	Confidence     float64                `json:"confidence"`
	Entities       map[string]interface{} `json:"entities"` // Good for flexibility
	RequiredAction ActionType             `json:"required_action"`
}

type IntentType string

const (
	IntentInitialRequest    IntentType = "initial_request" // Might not be needed if StartNewSession is distinct
	IntentAddPOI            IntentType = "add_poi"
	IntentRemovePOI         IntentType = "remove_poi"
	IntentModifyItinerary   IntentType = "modify_itinerary" // General modification
	IntentChangePreferences IntentType = "change_preferences"
	IntentAskQuestion       IntentType = "ask_question"
	IntentClarification     IntentType = "clarification"    // Bot asks user for clarification
	IntentProvideFeedback   IntentType = "provide_feedback" // User gives feedback
	IntentChitChat          IntentType = "chit_chat"        // Non-task oriented conversation
	// Add more specific intents as your bot's capabilities grow
	IntentGetPOIDetails   IntentType = "get_poi_details"
	IntentFindHotels      IntentType = "find_hotels"
	IntentFindRestaurants IntentType = "find_restaurants"
	IntentReplacePOI      IntentType = "replace_poi" // More specific than general modify
	IntentChangeDate      IntentType = "change_date"
	IntentChangeLocation  IntentType = "change_location" // For the whole trip or part of it
	IntentSortItinerary   IntentType = "sort_itinerary"  // e.g., "sort by distance", "optimize for morning"
)

type ActionType string

const (
	ActionGenerateNew          ActionType = "generate_new"          // e.g., new itinerary, new POI details from scratch
	ActionUpdateExisting       ActionType = "update_existing"       // e.g., modify current itinerary, update preferences
	ActionProvideInfo          ActionType = "provide_info"          // e.g., answer a question, get POI details
	ActionRequestClarification ActionType = "request_clarification" // Bot needs more info from user
	ActionStoreFeedback        ActionType = "store_feedback"
	ActionNoOp                 ActionType = "no_op"             // For chit-chat or unhandled
	ActionExecuteDBQuery       ActionType = "execute_db_query"  // For simple info retrieval
	ActionCallExternalAPI      ActionType = "call_external_api" // e.g., weather, flight status
)

// StreamEvent represents different types of streaming events
type StreamEvent struct {
	Type       string          `json:"type"`
	Message    string          `json:"message"`
	Data       interface{}     `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
	Timestamp  time.Time       `json:"timestamp"`
	EventID    string          `json:"event_id"`
	IsFinal    bool            `json:"is_final,omitempty"`
	Navigation *NavigationData `json:"navigation,omitempty"`
}

// NavigationData contains information for URL navigation
type NavigationData struct {
	URL         string            `json:"url"`
	RouteType   string            `json:"route_type"`   // "itinerary", "restaurants", "activities", "hotels"
	QueryParams map[string]string `json:"query_params"` // sessionId, cityName, domain, etc.
}

// StreamEventType constants
const (
	EventTypeStart           = "start"
	EventTypeProgress        = "progress"
	EventTypeCityData        = "city_data"
	EventTypeGeneralPOI      = "general_poi"
	EventTypePersonalizedPOI = "personalized_poi"
	EventTypeItinerary       = "itinerary"
	EventTypeMessage         = "message"
	EventTypeError           = "error"
	EventTypeComplete        = "complete"
	EventTypeDomainDetected  = "domain_detected"
	EventTypePromptGenerated = "prompt_generated"
	EventTypeParsingResponse = "parsing_response"
	EventTypeUnifiedChat     = "unified_chat"
	EventTypeHotels          = "hotels"
	EventTypeRestaurants     = "restaurants"
	EventTypeChunk           = "chunk" // For immediate text chunks (Google GenAI pattern)
)

// StreamingResponse wraps the streaming channel and metadata
type StreamingResponse struct {
	SessionID uuid.UUID
	Stream    <-chan StreamEvent
	Cancel    context.CancelFunc
}

// SessionPerformanceMetrics contains performance-related metrics for chat sessions
type SessionPerformanceMetrics struct {
	AvgResponseTimeMs int      `json:"avg_response_time_ms"`
	TotalTokens       int      `json:"total_tokens"`
	PromptTokens      int      `json:"prompt_tokens"`
	CompletionTokens  int      `json:"completion_tokens"`
	ModelsUsed        []string `json:"models_used"`
	TotalLatencyMs    int      `json:"total_latency_ms"`
}

// SessionContentMetrics contains content-related metrics for chat sessions
type SessionContentMetrics struct {
	TotalPOIs          int      `json:"total_pois"`
	TotalHotels        int      `json:"total_hotels"`
	TotalRestaurants   int      `json:"total_restaurants"`
	CitiesCovered      []string `json:"cities_covered"`
	HasItinerary       bool     `json:"has_itinerary"`
	ComplexityScore    int      `json:"complexity_score"` // 1-10 based on content richness
	DominantCategories []string `json:"dominant_categories"`
}

// SessionEngagementMetrics contains engagement-related metrics for chat sessions
type SessionEngagementMetrics struct {
	MessageCount          int           `json:"message_count"`
	ConversationDuration  time.Duration `json:"conversation_duration"`
	UserMessageCount      int           `json:"user_message_count"`
	AssistantMessageCount int           `json:"assistant_message_count"`
	AvgMessageLength      int           `json:"avg_message_length"`
	PeakActivityTime      *time.Time    `json:"peak_activity_time,omitempty"`
	EngagementLevel       string        `json:"engagement_level"` // "low", "medium", "high"
}

// ChatSessionsResponse represents paginated chat sessions response
type ChatSessionsResponse struct {
	Sessions []ChatSession `json:"sessions"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	Limit    int           `json:"limit"`
	HasMore  bool          `json:"has_more"`
}
