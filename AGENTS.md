<claude-mem-context>
# Memory Context

# [loci-connect-server] recent context, 2026-07-11 1:19am GMT+1

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision 🚨security_alert 🔐security_note
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 50 obs (16,919t read) | 158,485t work | 89% savings

### May 30, 2026
S719 golang-performance: T2-2 WithTx transaction helper migration — poi, user, and chat domains (May 30 at 8:34 AM)
S878 Commit all pending changes, merge fcorreia/improve-codebase into main, and push to origin — loci-connect-server (FACorreiaa/loci-connect-api) (May 30 at 8:59 AM)
### Jun 3, 2026
S884 Production infrastructure decision: CICD pipeline with self-hosted DBs vs. Neon Postgres + Go on VPS — blocked by TimescaleDB/PostGIS extension requirements (Jun 3 at 10:28 AM)
S885 Scaffold production VPS deployment for loci-connect-server: docker-compose.prod.yaml, GitHub Actions CD pipeline, env template, and runbook (Jun 3 at 11:45 AM)
S895 Post-launch cleanup: Stripe enum fix, review enrichment, proto GetRecentReviews RPC, ops runbook, payments ADR, and client reviews wiring (Jun 3 at 11:50 AM)
S908 Add ErrorView error state handling to lists and favorites routes in loci-client, completing error state coverage across all collection pages (Jun 3 at 11:59 PM)
### Jun 4, 2026
S922 Implement GetRecentReviews end-to-end after new buf schema push — update loci-client and continue (Jun 4 at 9:40 AM)
12452 10:58a ✅ loci-client TypeScript typecheck passes cleanly after GetRecentReviews wiring
12453 " ✅ loci-client GetRecentReviews wiring committed and pushed to main
12454 10:59a ✅ Task 14 marked completed: GetRecentReviews end-to-end implementation done
S924 Fix flaky auth disconnections (login → near-me/chat → logout) and add e2e tests to prevent regressions (Jun 4 at 10:59 AM)
12460 11:08a 🔵 Auth Instability: Disconnections After Login on Key Routes
12461 " 🔵 JWT Auth Interceptor Architecture in loci-connect-server
12462 11:09a 🔵 Client-Side Auth Transport: Token Refresh Interceptor with Shared In-Flight Promise
12463 " 🔵 Auth Service Has No ValidateSession Method
12464 11:10a 🔵 Refresh Token Rotation: Server Deletes Old Session Before Issuing New Pair
12466 " 🔵 E2E Test Harness Already Exists in loci-connect-server
12468 " 🔵 E2E Tests Gated Behind //go:build e2e Build Tag
12469 11:11a 🔵 Existing E2E Auth Test Coverage: Token Presence Check Only
12470 " 🟣 Added TestE2E_AuthLifecycle: Full Token Lifecycle E2E Test
12471 11:12a 🟣 TestE2E_AuthLifecycle Passes on First Run
12472 " 🟣 Auth Lifecycle E2E Test Committed and Pushed to Main
12473 " 🔵 Client Auth Logout Triggers: AuthContext Handles onAuthExpired Event from Transport
12474 11:13a 🔵 Two Parallel Token Refresh Paths in Client Can Race During Session Restore
12475 11:14a 🔵 Confirmed Race: AuthContext.refreshToken and Transport Interceptor Both Use Same Refresh Token
12476 " 🔴 Fixed Auth Race: Removed Duplicate Refresh in AuthContext Session Restoration
12477 11:15a 🔵 Dead Code Remaining in AuthContext After Refresh Removal
12478 " 🔴 Removed Unused getRefreshToken Import from AuthContext.tsx
12479 " 🔴 Auth Double-Refresh Race Fix Shipped to wanderwise-ai-client Main
S928 Harden the chat-stream 401 path — fix token-expiry disconnects in server-streaming gRPC chat (Jun 4 at 11:16 AM)
12492 11:49a 🟣 Hardened chat-stream endpoint 401 error path
12493 " 🔵 loci-client llm.ts streaming architecture mapped
12494 11:50a 🔵 StartChatStreamReal catch block lacks 401 differentiation
12495 " 🔵 sendUnifiedChatMessageStream is the top-level chat-stream entry point with no 401 handling
12496 " 🟣 Exported refreshSession() from connect-transport.ts for streaming 401 use
12497 " 🔵 llm.ts imports transport but not ConnectError/Code — missing for 401 handling
12498 " ✅ Added ConnectError and Code imports to llm.ts
12499 11:51a ✅ Imported refreshSession into llm.ts completing prerequisite imports for 401 hardening
12500 " 🟣 Implemented streamWithAuthRetry — one-shot auth recovery for server-streaming RPCs
12501 " 🔴 StartChatStreamReal now uses streamWithAuthRetry for 401 recovery
12503 " 🔵 Only one streamChat call site exists; ContinueChatStreamReal uses unary continueChat, not streaming
12504 " 🔵 TypeScript typecheck passes clean after chat-stream 401 hardening
12507 11:52a 🟣 Chat-stream 401 hardening committed and pushed to main (d0383f4)
### Jun 8, 2026
13672 11:54p ⚖️ New Plan: Generate OpenAPI + Bruno Collection from Proto Source of Truth
13673 " 🔵 loci-connect-proto buf.gen.yaml Plugin Configuration
13677 11:56p 🔵 buf BSR Authentication Confirmed Active; Proto Lint Warnings in auth.proto
13678 " ✅ Two Implementation Tasks Created for OpenAPI + Bruno Collection Generation
13680 " 🟣 Added sudorandom-connect-openapi Plugin to buf.gen.yaml
13682 11:57p 🟣 OpenAPI v3.1 Spec Successfully Generated from Proto at gen/openapi/loci.openapi.yaml
13684 " 🔵 openapi-to-bruno CLI Deprecated and Incompatible; Different CLI Syntax Required
13685 11:58p 🔵 Bruno CLI (@usebruno/cli v3.4.2) Supports Native OpenAPI Import
13687 " 🟣 Bruno Collection Successfully Generated from OpenAPI Spec — 158 .bru Files Across All Services
13689 11:59p 🔵 Generated Bruno .bru Files Use {{baseUrl}} Variable; No Environments Directory Created
13691 " 🟣 Bruno Local Environment File Created with baseUrl for Connect Server
13692 " 🔵 loci-connect-proto Makefile Structure — Existing Targets Including Insomnia Binary Export
13694 " ✅ Added `bruno` to Makefile .PHONY Declaration
13695 " 🟣 Added `make bruno` Target to loci-connect-proto Makefile
### Jun 9, 2026
13696 12:00a ✅ Committed OpenAPI Generation + Bruno Makefile Target to loci-connect-proto
13697 " 🟣 loci-connect-proto OpenAPI + Bruno Changes Pushed to GitHub Main
S1066 Wire proto → OpenAPI → Bruno collection generation pipeline for loci-connect-server / loci-connect-proto (Jun 9 at 12:00 AM)
**Investigated**: - `loci-connect-proto/buf.gen.yaml` — existing plugin configuration (Go, Connect-Go, TypeScript ES, pseudomuto-doc; Kotlin commented out; no OpenAPI plugin)
    - `loci-collection/` — hand-curated Bruno collection with `bruno.json` and `requests/` subdirectory
    - `loci-connect-proto/Makefile` — existing targets: lint, generate, push, insomnia (hardcoded absolute path), ontology
    - buf BSR auth state — confirmed logged in as `facorreiaa`, BUF_TOKEN valid
    - buf lint output — passes (exit 0) with 3 redundant `IGNORE_IF_ZERO_VALUE` warnings in `proto/loci/auth/auth.proto` (lines 66, 91, 95)
    - `openapi-to-bruno` npm package — found to be deprecated v1.0.0, incompatible CLI flags (`--input` not recognized)
    - `@usebruno/cli@3.4.2` — confirmed `bru import openapi` with `-s/--source`, `-o/--output`, `--collection-format bru`, `--group-by tags` flags

**Learned**: - The Loci stack is Connect/protobuf — proto is the source of truth; Go/TS types are already generated; no OpenAPI→Go codegen needed
    - Streaming RPCs (`ChatService.StreamChat`, `StatisticsService.StreamMainPageStatistics`) cannot be represented in OpenAPI v3.1 — only unary endpoints appear in the generated spec
    - `openapi-to-bruno` npm package is deprecated and non-functional; the correct tool is `@usebruno/cli import openapi` with `--collection-format bru`
    - Generated Bruno `.bru` files use `{{baseUrl}}` variable — requires a manually-created `environments/` directory; none is auto-generated by the CLI
    - Connect RPC path convention in OpenAPI: `POST /loci.auth.AuthService/Login` with required `Connect-Protocol-Version: 1` header
    - `loci-connect-server` listens on port 8000 locally
    - The existing Makefile had an `insomnia` target (proto binary export) indicating prior API tooling history before Bruno
    - `buf generate` output to `gen/openapi/` was untracked before this session; now committed to git

**Completed**: - Added `buf.build/community/sudorandom-connect-openapi:v0.25.1` plugin to `loci-connect-proto/buf.gen.yaml` outputting `gen/openapi/loci.openapi.yaml` (format=yaml)
    - Ran `buf generate` successfully — produced 474KB OpenAPI 3.1.0 spec with 139 paths covering all unary Connect RPCs
    - Generated Bruno collection using `@usebruno/cli import openapi` into `loci-collection-generated/` — 21 service directories, 159 `.bru` files
    - Created `loci-collection-generated/environments/Local.bru` with `baseUrl: http://localhost:8000`
    - Added `bruno:` Makefile target to `loci-connect-proto/Makefile` (added to .PHONY + recipe using `@usebruno/cli`)
    - Committed all changes (buf.gen.yaml, Makefile, gen/openapi/loci.openapi.yaml) as commit `0030852` to `loci-connect-proto`
    - Pushed `0030852` to `github.com:FACorreiaa/loci-connect-proto.git` main branch
    - Tasks #19 (add OpenAPI plugin + generate spec) and #20 (generate Bruno collection) both marked completed

**Next Steps**: - Decide whether to replace `loci-collection/` with `loci-collection-generated/` (fully generated, proto-driven) or keep them side-by-side
    - If adopting: commit `loci-collection-generated/` into a tracked repo or replace `loci-collection/` contents
    - Optionally fix the 3 redundant `IGNORE_IF_ZERO_VALUE` buf lint warnings in `proto/loci/auth/auth.proto`
    - Optionally clean up the `insomnia` Makefile target (hardcoded absolute path)
    - Session appears complete for the OpenAPI/Bruno plan; prior backlog items (Reviews GetRecentReviews proto change, Stripe enum fix, Ops runbook) remain unstarted


Access 158k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>