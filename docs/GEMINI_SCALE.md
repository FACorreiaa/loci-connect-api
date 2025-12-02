# Gemini API Scaling & Cost Controls

- **Baseline**: Use a single org-level API key with internal rate limits and usage caps per tenant/user. Log requests, set hard/soft quotas, and tune prompt/response lengths to minimize tokens. Cache deterministic responses and reuse context windows.
- **When to isolate**: Issue per-tenant keys for noisy/heavy or compliance-sensitive tenants to get clean cost attribution and faster off-switches. Expect overhead: key provisioning, rotation, and routing by tenant.
- **Plan choice**: Start on standard; negotiate an extended/committed-use plan when aggregate volume becomes predictable to get better unit economics and higher rate limits.
- **Hybrid approach**: Default to the org key + metering; migrate heavy/enterprise tenants to dedicated keys or a higher-limit pool as they scale.
- **Model mix**: Prefer cheaper models when quality allows; reserve premium models for high-impact flows or fallbacks.
- **Guardrails**: Implement per-tenant rate limiting, usage alerts, and dashboards. Apply prompt truncation, output length limits, and batching where possible. Run traffic simulations to estimate token costs before plan changes.
