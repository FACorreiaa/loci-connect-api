# Loci MCP Contract

The inventory of what Loci's Model Context Protocol surface exposes, what each
tool is allowed to do, and the bounds it enforces.

This document is verified by `internal/mcp/contract_test.go`. When the code and
this file disagree, the test fails — so a tool cannot be added, renamed, or
reclassified without the change being deliberate and visible here.

## Transport and identity

| Property | Value |
|---|---|
| Endpoint | `POST /mcp` on the main API mux |
| Transport | Streamable HTTP, stateless |
| Server name | `loci` |
| Auth | `Authorization: Bearer loci_sk_...` — API key only, never a JWT |
| Rate limit | 60 requests/minute per key, burst 10 |
| Quota | Generation spends the owner's daily LLM quota, reset at midnight UTC |

The MCP endpoint sits outside the Connect interceptor chain. `authMiddleware` is
its entire auth story: it resolves the key, rejects revoked and expired ones,
applies the rate limit, and injects the owner's identity plus the key's scopes.

There is also a stdio client (`cmd/loci-mcp`) which is a pure proxy — it mirrors
whatever the remote server advertises and adds no tools of its own.

## Scopes

A key authenticates **as its owning user**. Scopes are the only thing narrowing
it from full account access.

| Scope | Grants |
|---|---|
| `read` | Retrieval and listing. Everything needed to answer a question. |
| `write` | Mutating the owner's saved data — favorites, lists, itineraries. |
| `write:generate` | Spending the daily LLM quota. Separate from `write` because it costs money per call. |

No scope implies another: a key that needs to read and write must be minted with
both. What a key can do is exactly what its row says.

Stored in `api_keys.scopes text[]`, constrained to the known set and to at least
one entry (migration `0074`). Scope checks **fail closed** — a request that
reaches a tool without passing through `authMiddleware` holds no scopes and is
refused.

`CreateApiKeyRequest.scopes` selects them at mint time. Omitting the field yields
a read-only key — the safe default, so a client that does not know about scopes
cannot accidentally hand out write access. An unrecognised scope is rejected
rather than dropped, because silently narrowing a grant would return a key weaker
than the caller believes they hold.

Keys that existed before scopes were introduced were backfilled to all three
(migration `0074`). Narrowing them at deploy time would have broken live
integrations with no warning and no way for the owner to know why.

## Tools

Thirteen tools. The `Writes` column is the answer to "can the key I just handed
out change my data?"

### Read-only — require `read`

| Tool | Purpose |
|---|---|
| `status` | What this key may do, the bounds every tool enforces, how results are grounded. |
| `search_pois` | Keyword + semantic search near a location. |
| `get_poi_details` | Full detail for one place by id. |
| `find_nearby` | Restaurants, hotels, activities or attractions within a radius. |
| `list_itineraries` | The caller's saved itineraries. |
| `get_itinerary` | One saved itinerary, including its markdown. |
| `list_user_lists` | The caller's saved lists. |
| `get_list` | One list with its items. |
| `list_favorites` | The caller's favorites. |

`find_nearby` is read-only from the caller's perspective but may trigger
enrichment writes to Loci's own POI corpus on first request for an area. It never
writes to the caller's data.

### Mutating — require `write`

| Tool | Effect |
|---|---|
| `update_itinerary` | Partial update of a saved itinerary; can publish or unpublish it. |
| `add_poi_to_list` | Adds a place to one of the caller's lists. |
| `add_favorite` | Adds a place to favorites. |

### Generating — requires `write:generate`

| Tool | Effect |
|---|---|
| `plan_itinerary` | Creates a chat session, generates an itinerary, saves it. Pro plan only; spends one quota unit. |

## Bounds

Shared constants from `internal/domain/retrieval/limits.go`, so a limit means the
same thing on the MCP surface, the Connect handlers and the chat path.

| Bound | Value | Applies to |
|---|---|---|
| `MaxEvidence` | 20 | Maximum results returned by any list tool |
| `MaxDescriptionChars` | 300 | Description truncation (rune-aware) |
| `MaxQueryChars` | 512 | Longest accepted search query |

A response that hit the result cap sets `truncated: true`, and `count` reports
the pre-truncation total.

## Result shape

Every POI result carries:

| Field | Meaning |
|---|---|
| `id` | Stable POI identifier. Omitted when the place has no stored row. |
| `match_reason` | Why this surfaced: `lexical`, `semantic`, `both`, `nearby`. |
| `source` | Where the underlying data came from, e.g. `llm`. |
| `distance_km` | **Only** on tools that performed a radius search. |
| `recommendation_trace` | Attribution for outcome reporting. Stripped rather than returned unsigned if the integrity gate rejects it. |

`distance_km` is deliberately absent elsewhere. It replaced a `distance_meters`
field populated from a struct member that holds kilometres on spatial code paths
and a raw cosine similarity score on vector ones — so agents were being handed a
similarity score labelled as a distance in metres.

## Errors

Typed service failures become MCP tool errors, never transport failures or a
server crash. An insufficient scope is a refused tool, not a broken connection —
the call authenticated correctly, it simply asked for something the key may not
do.

Quota exhaustion and throttling are translated in `internal/mcp/errors.go`, since
MCP has no header channel to carry a `Retry-After`.

## Changing this contract

1. Update the tool set or classification in `internal/mcp/scopes.go`.
2. Update this document.
3. Run `go test ./internal/mcp/` — the contract test asserts the running server
   exposes exactly the documented set with the documented classification.

A tool added without classifying it fails the test rather than shipping as an
unclassified capability.

## Deadlines and accounting

Every MCP request carries a deadline, set from `CHAT_RPC_TIMEOUT_SEC` (default
3 minutes) because `plan_itinerary` generates on the same path as a chat
request. The endpoint sits outside the Connect interceptor chain, so it
inherits none of the RPC timeouts, and the server's `WriteTimeout` is 0 for
streaming; without this a hung tool would hold its connection for as long as
the client waits.

The deadline is applied to the request context and observed by the tool. It is
deliberately not enforced by abandoning the handler on another goroutine:
net/http completes the request when `ServeHTTP` returns, so an abandoned tool
would write into a finished response, which is a data race. Buffering the body
instead would defeat the streaming transport.

Every tool call is recorded once, in `guardTool`:

- `loci_mcp_tool_calls_total{tool, outcome}` where outcome is `ok`, `denied`
  (scope refused) or `error`.
- A structured log line, message `mcp tool call`, carrying `tool`, `user_id`
  and `outcome`. The user lives in the log rather than a metric label because
  user ids are unbounded cardinality. Gate 2's "MCP tool calls by distinct
  non-owner users" is counted from this line.
