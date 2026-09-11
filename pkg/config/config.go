package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	// Load environment variables from .env files when present.
	_ "github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Subscription  SubscriptionConfig
	Stripe        StripeConfig
	Cache         CacheConfig
	Observability ObservabilityConfig
	Profiling     ProfilingConfig
	AI            AIConfig
	Secrets       SecretsConfig
	Messaging     MessagingConfig
	Voice         VoiceConfig
}

type CacheConfig struct {
	RedisURL   string
	KeyPrefix  string
	LLMTTL     time.Duration
	GeoTTL     time.Duration
	CleanupTTL time.Duration
	// GenerationsEnabled turns the durable generation cache (llm_generations)
	// on. Off means the chat service neither reads nor writes the table and
	// the in-memory store is the only cache — the kill-switch if a bad row
	// ever has to be stopped from being served without a deploy.
	GenerationsEnabled bool
}

const (
	AIProviderGemini     = "gemini"
	AIProviderOpenRouter = "openrouter"
)

// defaultFallbackModels are OpenRouter's zero-cost models, ordered by
// suitability. All advertise structured_outputs and response_format, which
// Loci's JSON-contract prompts require; the larger free models
// (nemotron-3-ultra, nemotron-3.5-lightning, both a million tokens of context)
// do not, so they are deliberately excluded despite the bigger window.
//
// z-ai/glm-5.2:free was the head here until OpenRouter retired it. Asking for
// it returns 404 "This model is unavailable for free. The paid version is
// available now", and the chain does not advance past that — so every non-Pro
// user's chat failed outright, in production, while the paid primary kept
// working for anyone on a key. A dead head takes the whole chain down with it,
// which is the argument for listing more than one live model rather than
// trusting any single free slug to stay free.
var defaultFallbackModels = []string{
	"nvidia/nemotron-3-super-120b-a12b:free",
	"nex-agi/nex-n2.5-pro:free",
}

// AIProviderSpec identifies one link in the chat fallback chain.
type AIProviderSpec struct {
	Provider string
	APIKey   string
	Model    string
}

// AIConfig holds provider-neutral chat and embedding configuration.
//
// Provider/APIKey/Model describe the primary chat provider. When
// FallbackEnabled is set, Fallbacks lists additional providers tried in
// order after the primary fails with a provider-side error, so the app
// keeps working without a funded key. Embeddings never fall back: no
// free embedding model exists, so retrieval degrades to lexical search
// instead.
type AIConfig struct {
	Provider           string
	APIKey             string
	Model              string
	FallbackEnabled    bool
	Fallbacks          []AIProviderSpec
	FallbackCooldown   time.Duration
	EmbeddingModel     string
	EmbeddingDimension int
	MaxConcurrentCalls int
	MaxRetries         int
	RetryBaseDelay     time.Duration
	RetryMaxDelay      time.Duration
	// GenerateTimeout caps a single non-streaming LLM call (including retries).
	GenerateTimeout time.Duration
	// StreamTimeout caps a full streaming LLM call from start to last chunk.
	StreamTimeout time.Duration
}

type ServerConfig struct {
	Host                 string
	Port                 int
	BaseURL              string
	RateLimitPerSecond   int
	RateLimitBurst       int
	IPRateLimitPerSecond int
	IPRateLimitBurst     int

	// TrustedProxies are the hops whose X-Forwarded-For may be believed, as
	// CIDRs or bare addresses (TRUSTED_PROXIES).
	//
	// Empty is safe but wrong behind an ingress: the per-IP limiter then keys
	// every request on the proxy's own address, so all callers share one bucket
	// and the first few to arrive spend the budget for everybody. Set it to the
	// pod network when running behind Traefik.
	//
	// Do NOT add a hop that does not overwrite the header — trusting one that
	// merely passes it through means accepting a rate-limit key from the caller.
	TrustedProxies []string

	// WarnNoTrustedProxies is set when production is configured with none, so
	// the router can say so once at boot instead of every request.
	WarnNoTrustedProxies    bool
	IPRateLimitMaxEntries   int
	UserRateLimitPerSecond  int
	UserRateLimitBurst      int
	UserRateLimitMaxEntries int
	DefaultRPCTimeout       time.Duration
	ChatRPCTimeout          time.Duration
	// ChatStreamMaxTimeout caps client-requested deadlines on streaming RPCs.
	ChatStreamMaxTimeout time.Duration
	// AllowedOrigins are the CORS origins permitted for browser clients.
	AllowedOrigins []string
}

type DatabaseConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type AuthConfig struct {
	JWTSecret string
	// JWTRefreshSecret signs refresh tokens. It must differ from JWTSecret:
	// with one shared key an access token verifies as a refresh token, so a
	// leaked access token could be exchanged for a fresh pair. Defaults to
	// JWTSecret so existing deployments keep booting, but production refuses
	// to start when the two are equal.
	JWTRefreshSecret string
	// Token lifetimes. These used to be hardcoded in dependencies.go while the
	// env vars sat in .env doing nothing, so the running config did not match
	// what the file claimed.
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AdminEmail      string

	// MFASecretKey encrypts TOTP secrets at rest. Must be exactly 32 bytes.
	// Empty disables MFA entirely rather than storing secrets in plaintext —
	// see internal/domain/mfa.
	MFASecretKey string
	// MFARequiredForRole is a comma-separated list of roles that cannot turn MFA
	// off, e.g. "admin,owner". Empty means MFA is optional for everyone.
	MFARequiredForRole string
}

// SecretsConfig holds the key material that seals user-supplied secrets at
// rest: bring-your-own-key provider credentials and external MCP access
// tokens. Distinct from AuthConfig.MFASecretKey, which seals one thing Loci
// generates itself.
type SecretsConfig struct {
	// EncryptionKey is ENCRYPTION_KEY in pkg/secret's format: comma-separated
	// "<id>:<base64 of 32 bytes>" entries with the active key first, or a bare
	// base64 key meaning id 1. Two entries is what rotation looks like.
	//
	// Empty is a supported state. It disables every feature that would
	// otherwise have to store a user's secret, which is the only honest
	// alternative to storing one in the clear.
	EncryptionKey string
}

// MessagingConfig holds the chat-platform bridge settings.
type MessagingConfig struct {
	// TelegramBotToken is the credential from BotFather. Empty disables the
	// bridge entirely — no poller starts and the settings page says so rather
	// than issuing link codes nothing can redeem.
	//
	// It travels in the Bot API's URL path, which is why nothing on that path
	// wraps an error that could carry it; see internal/domain/messaging/telegram.
	TelegramBotToken string

	// TelegramBotHandle is the "@name" people send their link code to. Shown in
	// settings; the bridge itself resolves the bot from the token.
	TelegramBotHandle string

	// TelegramWebhookSecret is the value given to Telegram's setWebhook, which
	// it echoes back in X-Telegram-Bot-Api-Secret-Token on every delivery.
	//
	// Its presence is what selects webhook mode; see UsesWebhook. Empty means
	// long polling, which needs no public address and is right for a laptop.
	// There is no separate "mode" variable on purpose: a mode switch with an
	// empty secret would be an unauthenticated endpoint, and no combination
	// of settings should be able to produce one.
	TelegramWebhookSecret string
}

// UsesWebhook reports whether Telegram delivers updates by POSTing to
// {BASE_URL}/webhooks/telegram rather than being polled.
//
// Derived from the secret alone. The two modes are mutually exclusive at
// Telegram's end — getUpdates is refused while a webhook is registered — so
// the poller does not start in this mode; see Dependencies.RunTelegram.
func (c MessagingConfig) UsesWebhook() bool { return c.TelegramWebhookSecret != "" }

// VoiceConfig holds speech in and speech out.
//
// Deliberately independent of AIConfig. Chat runs on OpenRouter in production
// and has to keep running on it; speech needs a Gemini key and Gemini's own
// speech models, and reading the key through AIConfig would mean a voice
// feature could only ship by moving every itinerary onto a different provider.
// loadAIConfig reads GEMINI_API_KEY only when AI_PROVIDER is "gemini", and
// loadFallbacks never reads it at all, so setting it here cannot pull Gemini
// into the chat chain by accident.
type VoiceConfig struct {
	// GeminiAPIKey is the credential for transcription and synthesis. Empty
	// disables both, the same way an empty bot token disables the bridge: a
	// deployment without it answers text and ignores recordings rather than
	// refusing to boot.
	GeminiAPIKey string

	// TranscribeModel hears recordings. SpeakModel says replies aloud, and is
	// a different model: Gemini's speech models are not its chat models, and
	// pointing this at a chat model produces a text answer rather than an
	// error. Both are configuration rather than constants because the speech
	// models are preview-tagged and get retired with little notice — a
	// retirement should be a ConfigMap edit, not a release.
	TranscribeModel string
	SpeakModel      string

	// VoiceName is one of Gemini's prebuilt voices.
	VoiceName string

	// MaxDuration bounds a voice note, MaxVideoDuration a round video message.
	// The video cap is shorter because a video note is billed as video — very
	// roughly three hundred tokens a second on top of the audio — so a minute
	// of footage costs an order of magnitude more than a minute of speech for
	// a transcript of the same words. Dropping the video track would need
	// ffmpeg, which is thirty times the size of the encoder already in the
	// image, so the answer is a shorter cap rather than a transcode.
	MaxDuration      time.Duration
	MaxVideoDuration time.Duration

	// MaxBytes bounds what is downloaded. Telegram refuses getFile past 20 MB,
	// so this matches rather than exceeds it.
	MaxBytes int64

	// RepliesEnabled turns spoken replies on. Off leaves transcription
	// working: recordings are still understood, the answer just comes back as
	// text. This is the switch to reach for if synthesis gets expensive — it
	// is a ConfigMap value, so flipping it needs a restart, not a release.
	RepliesEnabled bool

	// VideoNotesEnabled turns round video messages on, separately from voice
	// notes, because they cost differently. See MaxVideoDuration.
	VideoNotesEnabled bool

	// MaxConcurrentUpdates bounds how many updates are answered at once in
	// webhook mode, which is otherwise one unbounded goroutine per delivery.
	// Survivable while an update was one call to a model; not while each one
	// buffers audio through a transcription, a synthesis and an upload.
	MaxConcurrentUpdates int
}

// Enabled reports whether recordings can be understood at all.
func (c VoiceConfig) Enabled() bool { return c.GeminiAPIKey != "" }

// SubscriptionConfig holds daily LLM request quotas per plan tier.
// ProDailyLLMLimit is a hidden fair-use cap; Pro is marketed as unlimited.
type SubscriptionConfig struct {
	FreeDailyLLMLimit int
	ProDailyLLMLimit  int
	// ProEmails are accounts treated as Pro without a Stripe subscription —
	// the founder, testers, anyone comped. Comma-separated, matched
	// case-insensitively against users.email. Applied where the plan is
	// resolved, so quota, entitlements, model routing and the client all agree.
	ProEmails []string
}

// StripeConfig holds Stripe API credentials and the two Pro price IDs.
type StripeConfig struct {
	APIKey         string
	WebhookSecret  string
	PriceIDMonthly string
	PriceIDAnnual  string
}

type ObservabilityConfig struct {
	MetricsEnabled bool
	MetricsPort    int
}

type ProfilingConfig struct {
	Enabled bool
	Port    int
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Host:                    getEnv("SERVER_HOST", "localhost"),
			Port:                    getEnvAsInt("SERVER_PORT", 8080),
			BaseURL:                 getEnv("BASE_URL", "http://localhost:8080"),
			RateLimitPerSecond:      getEnvAsInt("SERVER_RATE_LIMIT_PER_SECOND", 100),
			RateLimitBurst:          getEnvAsInt("SERVER_RATE_LIMIT_BURST", 200),
			TrustedProxies:          getEnvAsSlice("TRUSTED_PROXIES", nil),
			IPRateLimitPerSecond:    getEnvAsInt("SERVER_IP_RATE_LIMIT_PER_SECOND", 30),
			IPRateLimitBurst:        getEnvAsInt("SERVER_IP_RATE_LIMIT_BURST", 60),
			IPRateLimitMaxEntries:   getEnvAsInt("SERVER_IP_RATE_LIMIT_MAX_ENTRIES", 10_000),
			UserRateLimitPerSecond:  getEnvAsInt("SERVER_USER_RATE_LIMIT_PER_SECOND", 20),
			UserRateLimitBurst:      getEnvAsInt("SERVER_USER_RATE_LIMIT_BURST", 40),
			UserRateLimitMaxEntries: getEnvAsInt("SERVER_USER_RATE_LIMIT_MAX_ENTRIES", 10_000),
			DefaultRPCTimeout:       getEnvAsDurationSeconds("DEFAULT_RPC_TIMEOUT_SEC", 30*time.Second),
			ChatRPCTimeout:          getEnvAsDurationSeconds("CHAT_RPC_TIMEOUT_SEC", 3*time.Minute),
			ChatStreamMaxTimeout:    getEnvAsDurationSeconds("CHAT_STREAM_MAX_TIMEOUT_SEC", 10*time.Minute),
			AllowedOrigins:          getEnvAsSlice("ALLOWED_ORIGINS", []string{"http://localhost:3000"}),
		},
		Database: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnvAsInt("DB_PORT", 5439),
			User:            getEnv("DB_USER", "postgres"),
			Password:        getEnv("DB_PASSWORD", "postgres"),
			Database:        getEnv("DB_NAME", "loci"),
			SSLMode:         getEnv("DB_SSLMODE", "disable"),
			MaxConns:        int32(getEnvAsInt("DB_MAX_CONNS", 25)),
			MinConns:        int32(getEnvAsInt("DB_MIN_CONNS", 5)),
			MaxConnLifetime: getEnvAsDurationSeconds("DB_MAX_CONN_LIFETIME_SEC", 5*time.Minute),
			MaxConnIdleTime: getEnvAsDurationSeconds("DB_MAX_CONN_IDLE_SEC", 10*time.Minute),
		},
		Auth: AuthConfig{
			JWTSecret:        getEnv("JWT_SECRET", "changeme"),
			JWTRefreshSecret: getEnv("JWT_REFRESH_SECRET", ""),
			AccessTokenTTL:   getEnvAsDuration("JWT_ACCESS_TOKEN_TTL", time.Hour),
			RefreshTokenTTL:  getEnvAsDuration("JWT_REFRESH_TOKEN_TTL", 30*24*time.Hour),
			AdminEmail:       getEnv("ADMIN_EMAIL", ""),

			MFASecretKey:       getEnv("MFA_SECRET_KEY", ""),
			MFARequiredForRole: getEnv("MFA_REQUIRED_FOR_ROLE", ""),
		},
		Subscription: SubscriptionConfig{
			FreeDailyLLMLimit: getEnvAsInt("FREE_DAILY_LLM_LIMIT", 10),
			ProDailyLLMLimit:  getEnvAsInt("PRO_DAILY_LLM_LIMIT", 100),
			ProEmails:         getEnvAsSlice("PRO_EMAILS", nil),
		},
		Secrets: SecretsConfig{
			EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		},
		Messaging: MessagingConfig{
			TelegramBotToken:      getEnv("TELEGRAM_BOT_TOKEN", ""),
			TelegramBotHandle:     getEnv("TELEGRAM_BOT_HANDLE", ""),
			TelegramWebhookSecret: strings.TrimSpace(getEnv("TELEGRAM_WEBHOOK_SECRET", "")),
		},
		Voice: VoiceConfig{
			GeminiAPIKey:         strings.TrimSpace(getEnv("GEMINI_API_KEY", "")),
			TranscribeModel:      getEnv("VOICE_TRANSCRIBE_MODEL", "gemini-2.5-flash"),
			SpeakModel:           getEnv("VOICE_TTS_MODEL", "gemini-2.5-flash-preview-tts"),
			VoiceName:            getEnv("VOICE_TTS_VOICE", "Kore"),
			MaxDuration:          getEnvAsDurationSeconds("VOICE_MAX_DURATION_SEC", 60*time.Second),
			MaxVideoDuration:     getEnvAsDurationSeconds("VOICE_MAX_VIDEO_NOTE_SEC", 30*time.Second),
			MaxBytes:             int64(getEnvAsInt("VOICE_MAX_BYTES", 20<<20)),
			RepliesEnabled:       getEnvAsBool("VOICE_REPLIES_ENABLED", true),
			VideoNotesEnabled:    getEnvAsBool("VOICE_VIDEO_NOTES_ENABLED", true),
			MaxConcurrentUpdates: getEnvAsInt("VOICE_MAX_CONCURRENT_UPDATES", 4),
		},
		Stripe: StripeConfig{
			APIKey:         getEnv("STRIPE_API_KEY", ""),
			WebhookSecret:  getEnv("STRIPE_WEBHOOK_SECRET", ""),
			PriceIDMonthly: getEnv("STRIPE_PRICE_ID_MONTHLY", ""),
			PriceIDAnnual:  getEnv("STRIPE_PRICE_ID_ANNUAL", ""),
		},
		Cache: CacheConfig{
			RedisURL:   getEnv("REDIS_URL", ""),
			KeyPrefix:  getEnv("CACHE_KEY_PREFIX", "loci:"),
			LLMTTL:     getEnvAsDurationSeconds("CACHE_LLM_TTL_SEC", 5*time.Minute),
			GeoTTL:     getEnvAsDurationSeconds("CACHE_GEO_TTL_SEC", 20*time.Minute),
			CleanupTTL: getEnvAsDurationSeconds("CACHE_CLEANUP_TTL_SEC", 10*time.Minute),

			GenerationsEnabled: getEnvAsBool("CACHE_GENERATIONS_ENABLED", true),
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: getEnvAsBool("METRICS_ENABLED", true),
			MetricsPort:    getEnvAsInt("METRICS_PORT", 9090),
		},
		Profiling: ProfilingConfig{
			Enabled: getEnvAsBool("PPROF_ENABLED", false),
			Port:    getEnvAsInt("PPROF_PORT", 6060),
		},
		AI: loadAIConfig(),
	}

	// TRUSTED_PROXIES decides which hops may set the address the per-IP rate
	// limiter keys on, so a typo silently disables that limiter's fairness.
	// Refused at boot rather than dropped.
	for _, entry := range cfg.Server.TrustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var err error
		if strings.Contains(entry, "/") {
			_, err = netip.ParsePrefix(entry)
		} else {
			_, err = netip.ParseAddr(entry)
		}
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES entry %q is not a valid address or CIDR", entry)
		}
	}
	cfg.Server.WarnNoTrustedProxies = IsProduction() && len(cfg.Server.TrustedProxies) == 0

	if cfg.AI.Provider != AIProviderGemini && cfg.AI.Provider != AIProviderOpenRouter {
		return nil, fmt.Errorf("unsupported AI_PROVIDER %q", cfg.AI.Provider)
	}
	// A missing primary key is survivable only when the fallback chain can
	// answer in its place. That is the whole point of the chain: the app
	// must stay testable with no provider account configured at all.
	if cfg.AI.APIKey == "" && len(cfg.AI.Fallbacks) == 0 {
		return nil, fmt.Errorf("%s is required", providerAPIKeyEnv(cfg.AI.Provider))
	}

	if cfg.AI.Model == "" && cfg.AI.APIKey != "" {
		return nil, fmt.Errorf("%s is required", providerModelEnv(cfg.AI.Provider))
	}

	// Guard against a dev default silently becoming the production model.
	//
	// This used to forbid the fallback chain outright in production, on the
	// grounds that free models are rate-limited and shared and so "a local
	// testing floor, never a production serving path". The rate-limit point
	// stands and is the reason for the shape below; the blanket ban went
	// further than it needed to, and also forbade using those models as a
	// floor for callers who have no provider of their own — which is the one
	// place a shared, throttled model is the right answer, because the
	// alternative is no answer.
	//
	// So the narrower rule: a free model must not be the PRIMARY, where its
	// shared bucket becomes every paying request's problem. Behind the
	// primary, and as the free tier's own chain, it is allowed.
	if IsProduction() {
		if strings.HasSuffix(cfg.AI.Model, ":free") {
			return nil, fmt.Errorf("%s must not be a :free model in production, got %q",
				providerModelEnv(cfg.AI.Provider), cfg.AI.Model)
		}

		// The opposite failure: a model that can cost anything. "openrouter/auto"
		// hands the choice to OpenRouter per request, and its catalogue runs
		// up to Claude Opus at about sixty times the default's price. Refused
		// rather than warned about, because the bill is the only other symptom
		// and it arrives a month later.
		if cfg.AI.Provider == AIProviderOpenRouter && strings.EqualFold(cfg.AI.Model, openRouterAutoModel) {
			return nil, fmt.Errorf("%s must name a model in production, not %q: "+
				"the router can pick any model, Claude Opus included; the default is %q",
				providerModelEnv(cfg.AI.Provider), openRouterAutoModel, DefaultOpenRouterModel)
		}

		// The spend ceiling, and the reason a dedicated key exists at all.
		//
		// AI_FALLBACK_OPENROUTER_API_KEY names the account that funds the free
		// floor — one bucket shared by every free-tier caller. Everything on it
		// must be zero-cost, or a single non-free entry bills the operator for
		// traffic the tier promises is free.
		//
		// Without that key the fallbacks run on the operator's own paid
		// account, which is a deliberate paid backstop rather than a floor, so
		// no ceiling applies.
		if getEnv("AI_FALLBACK_OPENROUTER_API_KEY", "") != "" {
			for _, spec := range cfg.AI.Fallbacks {
				if spec.Provider == AIProviderOpenRouter && !strings.HasSuffix(spec.Model, ":free") {
					return nil, fmt.Errorf("AI_FALLBACK_MODELS entry %q must be a :free model: "+
						"AI_FALLBACK_OPENROUTER_API_KEY funds the shared free floor", spec.Model)
				}
			}
		}

		// Same shape of guard, for a licence rather than a rate limit.
		//
		// Open-Meteo's free tier is non-commercial by their terms, and they
		// name "apps that have subscriptions" as an example of commercial use.
		// Loci sells a Pro plan, so a production deployment on the free tier is
		// a licence breach — and a silent one, because the API keeps answering.
		// Refusing to boot is the only thing that reliably surfaces it.
		//
		// Ways to satisfy this: set OPENMETEO_API_KEY (a paid subscription
		// grants a commercial licence), or use WEATHER_PROVIDER=openweather
		// with OPENWEATHER_API_KEY, which serves air quality too. See
		// deploy/OPS.md.
		if err := checkWeatherLicence(); err != nil {
			return nil, err
		}
	}
	if cfg.AI.EmbeddingModel == "" {
		return nil, fmt.Errorf("%s is required", providerEmbeddingModelEnv(cfg.AI.Provider))
	}
	if cfg.AI.EmbeddingDimension <= 0 {
		return nil, errors.New("AI_EMBEDDING_DIMENSION must be positive")
	}
	if cfg.AI.MaxRetries < 0 {
		return nil, errors.New("AI_MAX_RETRIES must be >= 0")
	}
	if cfg.AI.RetryBaseDelay < 0 || cfg.AI.RetryMaxDelay < 0 {
		return nil, errors.New("AI retry delays must be non-negative")
	}
	if cfg.AI.RetryMaxDelay > 0 && cfg.AI.RetryBaseDelay > cfg.AI.RetryMaxDelay {
		return nil, errors.New("AI_RETRY_BASE_DELAY must not exceed AI_RETRY_MAX_DELAY")
	}
	if cfg.Server.DefaultRPCTimeout <= 0 || cfg.Server.ChatRPCTimeout <= 0 {
		return nil, errors.New("RPC timeout values must be positive")
	}
	if cfg.AI.GenerateTimeout <= 0 || cfg.AI.StreamTimeout <= 0 {
		return nil, errors.New("AI timeout values must be positive")
	}

	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}
	// Refuse to boot with a known-insecure placeholder secret in ANY environment.
	// A weak secret in dev leaks into shared/staging deployments and forged tokens
	// are indistinguishable from real ones, so this is not production-only.
	switch cfg.Auth.JWTSecret {
	case "changeme", "replace-with-secure-env-var", "replace-with-secure-refresh-env-var":
		return nil, errors.New("JWT_SECRET must not be a default/placeholder value")
	}
	if len(cfg.Auth.JWTSecret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}

	// Fall back to the access secret so existing deployments keep working, but
	// say so, and refuse the shared-key setup outright in production.
	if cfg.Auth.JWTRefreshSecret == "" {
		cfg.Auth.JWTRefreshSecret = cfg.Auth.JWTSecret
	}
	if cfg.Auth.JWTRefreshSecret == cfg.Auth.JWTSecret && strings.EqualFold(getEnv("APP_ENV", "development"), "production") {
		return nil, errors.New("JWT_REFRESH_SECRET must be set and must differ from JWT_SECRET in production")
	}
	if len(cfg.Auth.JWTRefreshSecret) < 32 {
		return nil, errors.New("JWT_REFRESH_SECRET must be at least 32 characters")
	}

	if cfg.Auth.AccessTokenTTL <= 0 || cfg.Auth.RefreshTokenTTL <= 0 {
		return nil, errors.New("JWT token TTLs must be positive")
	}
	if cfg.Auth.RefreshTokenTTL <= cfg.Auth.AccessTokenTTL {
		return nil, errors.New("JWT_REFRESH_TOKEN_TTL must be longer than JWT_ACCESS_TOKEN_TTL")
	}

	return cfg, nil
}

func loadAIConfig() AIConfig {
	provider := strings.ToLower(strings.TrimSpace(getEnv("AI_PROVIDER", AIProviderOpenRouter)))
	cfg := AIConfig{
		Provider:           provider,
		EmbeddingDimension: getEnvAsInt("AI_EMBEDDING_DIMENSION", 768),
		MaxConcurrentCalls: getEnvAsIntFallback("AI_MAX_CONCURRENT_CALLS", "GEMINI_MAX_CONCURRENT_CALLS", 10),
		MaxRetries:         getEnvAsIntFallback("AI_MAX_RETRIES", "GEMINI_MAX_RETRIES", 3),
		RetryBaseDelay:     getEnvAsDurationMillisFallback("AI_RETRY_BASE_DELAY_MS", "GEMINI_RETRY_BASE_DELAY_MS", 500*time.Millisecond),
		RetryMaxDelay:      getEnvAsDurationMillisFallback("AI_RETRY_MAX_DELAY_MS", "GEMINI_RETRY_MAX_DELAY_MS", 8*time.Second),
		GenerateTimeout:    getEnvAsDurationSecondsFallback("AI_GENERATE_TIMEOUT_SEC", "GEMINI_GENERATE_TIMEOUT_SEC", 30*time.Second),
		StreamTimeout:      getEnvAsDurationSecondsFallback("AI_STREAM_TIMEOUT_SEC", "GEMINI_STREAM_TIMEOUT_SEC", 2*time.Minute),
	}

	switch provider {
	case AIProviderGemini:
		cfg.APIKey = getEnv("GEMINI_API_KEY", "")
		cfg.Model = getEnv("GEMINI_MODEL", "")
		cfg.EmbeddingModel = getEnv("GEMINI_EMBEDDING_MODEL", "gemini-embedding-001")
	case AIProviderOpenRouter:
		cfg.APIKey = getEnv("OPENROUTER_API_KEY", "")
		cfg.Model = getEnv("OPENROUTER_MODEL", DefaultOpenRouterModel)
		cfg.EmbeddingModel = getEnv("OPENROUTER_EMBEDDING_MODEL", "google/gemini-embedding-001")
	default:
		cfg.Provider = provider
	}

	cfg.FallbackEnabled = getEnvAsBool("AI_FALLBACK_ENABLED", !IsProduction())
	cfg.FallbackCooldown = getEnvAsDurationSeconds("AI_FALLBACK_COOLDOWN_SEC", 5*time.Minute)
	if cfg.FallbackEnabled {
		cfg.Fallbacks = loadFallbacks()
	}

	return cfg
}

// loadFallbacks builds the ordered fallback chain. Every entry is an
// OpenRouter model: the free tier is the only zero-cost provider wired
// into the app, and its key is read separately so it can be a spend-
// capped key distinct from the primary one.
func loadFallbacks() []AIProviderSpec {
	key := getEnv("AI_FALLBACK_OPENROUTER_API_KEY", getEnv("OPENROUTER_API_KEY", ""))
	if key == "" {
		return nil
	}

	models := getEnvAsSlice("AI_FALLBACK_MODELS", defaultFallbackModels)
	specs := make([]AIProviderSpec, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		specs = append(specs, AIProviderSpec{
			Provider: AIProviderOpenRouter,
			APIKey:   key,
			Model:    model,
		})
	}
	return specs
}

// DefaultOpenRouterModel is what OPENROUTER_MODEL falls back to.
//
// A named, cheap model rather than OpenRouter's own router. The default used
// to be "openrouter/auto", which lets OpenRouter pick per request from its
// whole catalogue — Claude Opus included, at roughly sixty times this model's
// price — so an unset variable could quietly bill every itinerary at premium
// rates. DeepSeek V4 Flash is $0.084/$0.168 per million tokens with a one
// million token context and structured outputs, which is enough for a plan.
//
// Also the catalogue default for a user's own OpenRouter key; see
// pkg/ai/providers. Same footgun, their money.
const DefaultOpenRouterModel = "deepseek/deepseek-v4-flash"

// openRouterAutoModel is OpenRouter's per-request router. Refused as the
// production primary; see the check in Load.
const openRouterAutoModel = "openrouter/auto"

// IsProduction reports whether the process is running as production.
func IsProduction() bool {
	return strings.EqualFold(getEnv("APP_ENV", "development"), "production")
}

func providerAPIKeyEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_API_KEY"
	}
	return "GEMINI_API_KEY"
}

// checkWeatherLicence refuses a production boot that would use Open-Meteo's
// non-commercial free tier.
//
// Read straight from the environment rather than from Config: these are
// localcontext's variables, and threading them through the config struct just
// to check them here would put weather-provider knowledge in two places.
func checkWeatherLicence() error {
	if strings.TrimSpace(os.Getenv("OPENMETEO_API_KEY")) != "" {
		return nil // paid plan; commercially licensed for both forecast and air quality
	}

	owKey := strings.TrimSpace(os.Getenv("OPENWEATHER_API_KEY"))
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("WEATHER_PROVIDER")))
	if provider == "" {
		// Matches NewWeatherAdapterFromEnv: an existing OpenWeather key wins
		// when no provider is named, otherwise Open-Meteo is the default.
		if owKey != "" {
			provider = "openweather"
		} else {
			provider = "openmeteo"
		}
	}

	switch provider {
	case "openweather":
		// The adapter falls back to Open-Meteo when the key is missing, which
		// would put us back on the free tier while the config still reads
		// "openweather". That fallback is right for a dev box and wrong here.
		if owKey == "" {
			return errors.New(openWeatherKeyErr)
		}
		// Air quality follows the weather provider, so this covers both.
		return nil

	case "stub":
		// The stub makes no external call, but air quality would still go to
		// Open-Meteo because it only follows *openweather*.
		if getEnvAsBool("AIR_QUALITY_ENABLED", true) {
			return errors.New(airQualityLicenceErr)
		}
		return nil

	default:
		return errors.New(openMeteoLicenceErr)
	}
}

const (
	openMeteoLicenceErr = "WEATHER_PROVIDER=openmeteo needs OPENMETEO_API_KEY in production: " +
		"Open-Meteo's free tier is non-commercial and Loci sells subscriptions"
	openWeatherKeyErr = "WEATHER_PROVIDER=openweather needs OPENWEATHER_API_KEY in production: " +
		"without it the adapter falls back to Open-Meteo, whose free tier is non-commercial"
	airQualityLicenceErr = "AIR_QUALITY_ENABLED must be false in production with this weather " +
		"provider: air quality would be served by Open-Meteo, whose free tier is non-commercial"
)

func providerModelEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_MODEL"
	}
	return "GEMINI_MODEL"
}

func providerEmbeddingModelEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_EMBEDDING_MODEL"
	}
	return "GEMINI_EMBEDDING_MODEL"
}

// getEnvAsSlice reads a comma-separated env var into a string slice.
func getEnvAsSlice(key string, defaultValue []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return defaultValue
	}
	return out
}

// DSN returns the database connection string
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsIntFallback(key, legacyKey string, defaultValue int) int {
	if os.Getenv(key) != "" {
		return getEnvAsInt(key, defaultValue)
	}
	return getEnvAsInt(legacyKey, defaultValue)
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultValue
}

// getEnvAsDuration parses a Go duration string ("15m", "168h"). Unlike the
// *Seconds helpers it takes the unit from the value, which is what the
// JWT_*_TTL variables have always been written as.
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(valueStr)
	if err != nil || d <= 0 {
		return defaultValue
	}
	return d
}

func getEnvAsDurationSeconds(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	seconds, err := strconv.Atoi(valueStr)
	if err != nil || seconds <= 0 {
		return defaultValue
	}
	return time.Duration(seconds) * time.Second
}

func getEnvAsDurationSecondsFallback(key, legacyKey string, defaultValue time.Duration) time.Duration {
	if os.Getenv(key) != "" {
		return getEnvAsDurationSeconds(key, defaultValue)
	}
	return getEnvAsDurationSeconds(legacyKey, defaultValue)
}

func getEnvAsDurationMillis(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	millis, err := strconv.Atoi(valueStr)
	if err != nil || millis < 0 {
		return defaultValue
	}
	return time.Duration(millis) * time.Millisecond
}

func getEnvAsDurationMillisFallback(key, legacyKey string, defaultValue time.Duration) time.Duration {
	if os.Getenv(key) != "" {
		return getEnvAsDurationMillis(key, defaultValue)
	}
	return getEnvAsDurationMillis(legacyKey, defaultValue)
}
