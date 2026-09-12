package aicreds

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Router is a ChatClient that picks, per call, between the caller's own
// provider and the one Loci runs.
//
// It is a wrapper rather than a change at each call site because the services
// reach their client from about a dozen places, most of which have no user id
// in scope — threading one through all of them to express "whose key is this"
// would be a far larger change than the feature. Every method on the interface
// that could need the answer already takes a context, and the context already
// carries the authenticated caller.
//
// The cost is that the choice is implicit. What keeps that honest: a caller
// with no credential, an unauthenticated context, and a credential that will
// not open all resolve to the shared client, so the failure mode is "runs on
// Loci's provider", never "fails".
type Router struct {
	shared generativeAI.ChatClient
	svc    *Service
	logger *slog.Logger

	// httpClient is shared by every user's client so a per-user provider gets
	// connection reuse instead of a TLS handshake per request. Gateway
	// providers wrap it in a dialer that re-checks the target; see
	// providers.GatewayHTTPClient.
	httpClient *http.Client

	// free serves callers with no provider of their own and no paid plan. Nil
	// when no free chain is configured, in which case the tier is moot and
	// everyone gets the shared chain — today's behaviour.
	free generativeAI.ChatClient
	// plans resolves a caller's effective plan. Nil disables tier routing.
	plans PlanLookup

	mu     sync.Mutex
	cached map[uuid.UUID]*cachedClient
	// planCache holds the answer briefly. A single chat turn makes several
	// model calls, and a subscriptions query per call would be a query per
	// token stream.
	planCache map[uuid.UUID]cachedPlan
}

// PlanLookup reports a user's effective plan. Satisfied by
// subscription.Service.
type PlanLookup interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

type cachedPlan struct {
	plan string
	at   time.Time
}

// planTTL is how long a plan answer is reused.
//
// Short: an upgrade should take effect while somebody is still looking at the
// screen that sold it to them. Long enough that a burst of model calls inside
// one turn asks once.
const planTTL = time.Minute

type cachedClient struct {
	client generativeAI.ChatClient
	// version is the credential's updated_at when this client was built. A
	// saved change moves it, which is what makes the next request pick up the
	// new key rather than the next deploy.
	version time.Time
}

var _ generativeAI.ChatClient = (*Router)(nil)

// NewRouter wraps shared so that users who brought their own key are served by
// it. A nil service, or one with no encryption key configured, yields a router
// that always returns shared — which is every account today.
func NewRouter(shared generativeAI.ChatClient, svc *Service, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		shared:     shared,
		svc:        svc,
		logger:     logger,
		httpClient: &http.Client{},
		cached:     make(map[uuid.UUID]*cachedClient),
		planCache:  make(map[uuid.UUID]cachedPlan),
	}
}

// WithFreeTier routes callers with no provider of their own and no paid plan to
// free, instead of the chain the operator pays for.
//
// Both arguments are required for the routing to engage: without a plan lookup
// there is no way to tell a free caller from a paying one, and guessing in
// either direction is worse than not routing at all.
func (r *Router) WithFreeTier(free generativeAI.ChatClient, plans PlanLookup) *Router {
	if free == nil || plans == nil {
		return r
	}
	r.free = free
	r.plans = plans
	return r
}

// clientFor answers which client serves this call.
//
// Every failure here returns the shared client. A broken credential must
// degrade the account to Loci's provider, not break it: the alternative is
// that one bad key saved in settings makes every itinerary request fail.
func (r *Router) clientFor(ctx context.Context) generativeAI.ChatClient {
	rawID, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		// Background work and unauthenticated paths have no caller, so there
		// is no credential to find and no plan to read.
		return r.shared
	}
	userID, err := uuid.Parse(rawID)
	if err != nil {
		return r.shared
	}

	// Tier routing does not depend on bring-your-own-key. With no encryption
	// key configured there are no stored credentials to consult, but a free
	// caller should still be served by the free chain rather than the one the
	// operator pays for.
	if r.svc == nil || !r.svc.Enabled() {
		return r.chainForPlan(ctx, userID)
	}

	resolved, err := r.svc.Resolve(ctx, userID)
	switch {
	case errors.Is(err, ErrNotFound):
		// The ordinary case: this account did not bring a key. It is also what
		// a deletion looks like, so drop any client still cached from before
		// it — otherwise that client would hold connections to a provider the
		// user has stopped paying for until the process exits.
		r.Forget(userID)
		return r.chainForPlan(ctx, userID)
	case err != nil:
		// A stored credential that will not open — the encryption key was
		// rotated out from under it, or the address it names is no longer one
		// Loci will dial. Recorded, not only logged: this is the failure the
		// owner can actually fix, and without a note the settings page would
		// go on showing their key as in use while every request quietly ran on
		// ours.
		r.recordFailure(ctx, userID, err)
		return r.shared
	}

	client, err := r.cachedFor(ctx, userID, resolved)
	if err != nil {
		r.recordFailure(ctx, userID, err)
		return r.shared
	}
	return client
}

// chainForPlan picks between the free chain and the one the operator pays for.
//
// A brought key never reaches here — it outranks the tier, because the user is
// paying the provider directly and the plan says nothing about which backend
// should answer.
//
// Every uncertainty resolves to the free chain. An unreadable plan must not
// spend the operator's tokens: billing somebody on a database error is a worse
// mistake than serving them a cheaper model.
func (r *Router) chainForPlan(ctx context.Context, userID uuid.UUID) generativeAI.ChatClient {
	if r.free == nil || r.plans == nil {
		return r.shared
	}

	plan, err := r.effectivePlan(ctx, userID)
	if err != nil {
		r.logger.WarnContext(ctx, "could not read the caller's plan; serving the free chain",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
		return r.free
	}
	if subscription.IsProPlan(plan) {
		return r.shared
	}
	return r.free
}

// effectivePlan reads the plan, reusing a recent answer.
func (r *Router) effectivePlan(ctx context.Context, userID uuid.UUID) (string, error) {
	now := time.Now()

	r.mu.Lock()
	if hit, ok := r.planCache[userID]; ok && now.Sub(hit.at) < planTTL {
		r.mu.Unlock()
		return hit.plan, nil
	}
	r.mu.Unlock()

	plan, err := r.plans.EffectivePlan(ctx, userID)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	r.planCache[userID] = cachedPlan{plan: plan, at: now}
	r.mu.Unlock()
	return plan, nil
}

// recordFailure notes why this credential did not serve the request.
//
// Best effort, and deliberately not fatal: it runs on a path that has already
// fallen back, and a database that will not take the note must not also break
// the request that was about to succeed on the shared provider.
func (r *Router) recordFailure(ctx context.Context, userID uuid.UUID, cause error) {
	r.logger.WarnContext(ctx, "falling back to the shared provider",
		slog.String("user_id", userID.String()),
		slog.String("error", cause.Error()))

	if err := r.svc.RecordFailure(ctx, userID, cause.Error()); err != nil {
		r.logger.WarnContext(ctx, "could not record provider failure",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
	}
}

// cachedFor returns a client for this credential, building one if the cached
// entry is missing or stale.
//
// Clients are cached because building one is not free — a Gemini client opens
// its own transport, and one built per request would be a leak as much as an
// expense.
func (r *Router) cachedFor(ctx context.Context, userID uuid.UUID, resolved Resolved) (generativeAI.ChatClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cached[userID]; ok {
		if entry.version.Equal(resolved.Version) {
			return entry.client, nil
		}
		// The credential changed. Close the client built from the old one
		// before dropping it, or its connections outlive the key.
		if err := entry.client.Close(); err != nil {
			r.logger.WarnContext(ctx, "could not close a replaced provider client",
				slog.String("error", err.Error()))
		}
		delete(r.cached, userID)
	}

	client, err := ai.NewUserChatClient(ctx, ai.UserSpec{
		Provider:   resolved.Provider,
		Model:      resolved.Model,
		BaseURL:    resolved.BaseURL,
		APIKey:     resolved.APIKey,
		HTTPClient: r.httpClient,
		Logger:     r.logger,
	})
	if err != nil {
		return nil, err
	}

	r.cached[userID] = &cachedClient{client: client, version: resolved.Version}
	return client, nil
}

// Forget drops the cached client for a user.
//
// Called when a credential resolves to nothing, which is what a deletion looks
// like from here. Routing is already correct without it — Resolve reports
// ErrNotFound and the account falls back before the cache is consulted — so
// this exists to release the connections, not to change the answer. That is
// also why no caller has to remember to invoke it.
func (r *Router) Forget(userID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cached[userID]; ok {
		_ = entry.client.Close()
		delete(r.cached, userID)
	}
}

func (r *Router) Generate(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return r.clientFor(ctx).Generate(ctx, prompt, config)
}

func (r *Router) GenerateText(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error) {
	return r.clientFor(ctx).GenerateText(ctx, prompt, config)
}

func (r *Router) GenerateStream(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return r.clientFor(ctx).GenerateStream(ctx, prompt, config)
}

func (r *Router) StartChatSession(ctx context.Context, config *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return r.clientFor(ctx).StartChatSession(ctx, config)
}

// Model reports the shared provider's model.
//
// The interface gives it no context, so there is no caller to resolve. It is
// read for logging and metrics rather than to route a request, and answering
// with the shared model is better than guessing at a user's.
func (r *Router) Model() string { return r.shared.Model() }

// ModelFor names the model a call made with ctx would be answered by: the
// caller's own provider, the free chain, or the shared one, by the same rules
// clientFor applies. Model reports the shared provider whoever asks, which is
// the right answer for a process-wide label and the wrong one for anything
// keyed per request — a cache key built from it would serve one model's
// answer under another's name.
func (r *Router) ModelFor(ctx context.Context) string { return r.clientFor(ctx).Model() }

// PlanFor names the plan a call made with ctx would be billed under, by the
// same rules chainForPlan applies and out of the same short-lived cache.
//
// It exists because the size of an answer now depends on the plan — Pro asks
// the model for more places than free — and that resolved number goes into the
// generation cache key. Reading the plan through this method rather than
// separately is what keeps the two decisions consistent: a request routed to
// the Pro chain is a request sized for Pro.
//
// Ordinarily this costs nothing. Any turn that has already chosen a model has
// populated planCache, and planTTL is a minute. Where free-tier routing is not
// configured, chainForPlan returns before it ever reads a plan, so this is the
// first caller and does pay one read per user per minute — bounded, and worth
// stating rather than claiming a guarantee that does not hold everywhere.
//
// Every uncertainty resolves to free, for the reason chainForPlan gives:
// serving a slightly shorter list on a database error is a better mistake than
// billing somebody for a longer one.
func (r *Router) PlanFor(ctx context.Context) string {
	rawID, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return subscription.PlanFree
	}
	userID, err := uuid.Parse(rawID)
	if err != nil {
		return subscription.PlanFree
	}
	if r.plans == nil {
		return subscription.PlanFree
	}

	plan, err := r.effectivePlan(ctx, userID)
	if err != nil {
		r.logger.WarnContext(ctx, "could not read the caller's plan; sizing the answer for free",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
		return subscription.PlanFree
	}
	return plan
}

// Close closes the shared client and every client built for a user.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for userID, entry := range r.cached {
		if err := entry.client.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(r.cached, userID)
	}
	if err := r.shared.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
