// Package providers describes the model backends a user may point their own
// key at, and the rules for dialing one they host themselves.
//
// Distinct from pkg/config's provider list, which answers "what can this
// server build from its own environment". This one answers "what may a person
// bring a key for", and the two lists are deliberately different sizes.
package providers

// BYOProvider is one backend a user may bring their own key for.
//
// Data, not configuration: each entry needs a base URL and a dialect somebody
// verified, so adding one is a code change rather than an environment
// variable. That is also why there is no "custom, enter your own base URL"
// entry — an arbitrary URL is a request Loci would make on the user's behalf
// to an address nobody checked. Hermes is the one exception, and it is fenced
// by ParseGatewayURL.
type BYOProvider struct {
	// Name is what user_ai_credentials.provider stores.
	Name string

	// Label is how it is written for a person.
	Label string

	// BaseURL is empty for Gemini, which is not an OpenAI-dialect backend and
	// is reached through its own SDK (pkg/gemini). Empty for Hermes too, for
	// the opposite reason: the address belongs to the user.
	BaseURL string

	// DefaultModel is used when the user leaves the model blank. It exists
	// because the clients refuse an empty model, so "use the provider's
	// default" has to be spelled out somewhere.
	DefaultModel string

	// KeyHint is the shape of the credential, shown as the field placeholder
	// so somebody can tell at a glance whether they pasted the right thing.
	KeyHint string

	// VerifyPath is a cheap GET, relative to BaseURL, that answers 401 or 403
	// when the key is wrong. It exists to tell somebody their key is bad while
	// they are still looking at the form.
	//
	// Empty means no such endpoint was found for this provider, and the key is
	// stored unverified. That is the honest state for three of the six:
	//
	//   openrouter  /key      401 on a bad key. NOT /models, which is public
	//                         and answers 200 to anything.
	//   openai      /models   401 on a bad key.
	//   nvidia      —         /models is public; answers 200 to anything.
	//   xai         —         /models and /api-key both answer 400, which is
	//                         indistinguishable from a malformed request, so
	//                         treating it as rejection would refuse good keys.
	//   gemini      —         not an OpenAI-dialect backend; no equivalent
	//                         endpoint under a shared base URL.
	//   hermes      /models   the gateway serves the OpenAI dialect.
	//
	// These were probed with a deliberately invalid key rather than assumed.
	// Re-probe before adding one.
	VerifyPath string

	// Note is one line under the option in the settings page.
	Note string

	// RequiresBaseURL means this backend has no public address: the user names
	// theirs. Only Hermes. The URL is validated before Loci will dial it, and
	// again at connect time; see ParseGatewayURL and GatewayHTTPClient.
	RequiresBaseURL bool
}

// Catalog is the set of backends a person may bring a key for.
//
// Anthropic is deliberately absent. Its native API is a different dialect, and
// its OpenAI-compatibility endpoint is a documented shim with parity caveats —
// not something to ship on the strength of a plan. Claude is reachable today
// through OpenRouter with a model beginning "anthropic/".
var Catalog = []BYOProvider{
	{
		Name: "openrouter", Label: "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "anthropic/claude-sonnet-4.5",
		KeyHint: "sk-or-v1-…", VerifyPath: "/key",
		Note: "One key, most models — including Claude and GPT. The simplest choice.",
	},
	{
		Name: "openai", Label: "OpenAI",
		BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4.1",
		KeyHint: "sk-…", VerifyPath: "/models",
		Note: "Needs no new code: Loci's OpenRouter client already speaks OpenAI's dialect.",
	},
	{
		Name: "xai", Label: "xAI (Grok)",
		BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-4",
		KeyHint: "xai-…",
	},
	{
		Name: "nvidia", Label: "NVIDIA",
		BaseURL: "https://integrate.api.nvidia.com/v1", DefaultModel: "meta/llama-3.3-70b-instruct",
		KeyHint: "nvapi-…",
		Note:    "Free models, on an OpenAI-dialect endpoint.",
	},
	{
		// No VerifyPath: Gemini is not an OpenAI-dialect backend and has no
		// equivalent endpoint under a shared base URL, so its keys are stored
		// without a live check.
		Name: "gemini", Label: "Google Gemini",
		DefaultModel: "gemini-2.5-pro", KeyHint: "AIza…",
	},
	{
		// BaseURL is supplied per user: each person runs their own gateway.
		// The catalogue cannot name one address.
		Name: "hermes", Label: "Hermes (your gateway)",
		DefaultModel: "hermes-3", KeyHint: "gateway API_SERVER_KEY",
		VerifyPath: "/models", RequiresBaseURL: true,
		Note: "Your own Hermes instance — tailnet, LAN, or public. URL and key stay on this account.",
	},
}

// ByName finds a catalogue entry. The second return distinguishes "not a
// provider we support" from a zero-valued entry, which callers must not dial.
func ByName(name string) (BYOProvider, bool) {
	for _, p := range Catalog {
		if p.Name == name {
			return p, true
		}
	}
	return BYOProvider{}, false
}
