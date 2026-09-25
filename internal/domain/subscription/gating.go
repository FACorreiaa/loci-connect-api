package subscription

import "sync/atomic"

// Plan gating: whether a free account is held to the free feature set (Day-1
// exports, list and place caps, compare candidates, POI counts) or gets
// everything Pro gets. Off until the product has users worth gating; the
// decision and the order features will be gated in live in the iOS roadmap
// ("Deferred — Pro gating") and docs/pricing.md. What a plan *is* never
// changes with this switch: IsProPlan stays truthful for billing, the daily
// LLM quota and model routing, which are cost controls rather than features.
var gating atomic.Bool

// SetGating turns plan gating on or off (PLAN_GATING in config).
func SetGating(on bool) { gating.Store(on) }

// Gating reports whether free accounts are held to the free feature set.
func Gating() bool { return gating.Load() }

// Entitled reports whether a plan gets the Pro feature set: every plan while
// gating is off, only Pro plans while it is on. Feature gates call this, not
// IsProPlan.
func Entitled(plan string) bool { return !Gating() || IsProPlan(plan) }
