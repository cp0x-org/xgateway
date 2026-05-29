# CLAUDE.md

This file provides rules and guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Rules
These rules apply to every task in this project unless explicitly overridden.
Bias: caution over speed on non-trivial work. Use judgment on trivial tasks.

## Rule 1 — Think Before Coding
State assumptions explicitly. If uncertain, ask rather than guess.
Present multiple interpretations when ambiguity exists.
Push back when a simpler approach exists.
Stop when confused. Name what's unclear.

## Rule 2 — Simplicity First
Minimum code that solves the problem. Nothing speculative.
No features beyond what was asked. No abstractions for single-use code.
Test: would a senior engineer say this is overcomplicated? If yes, simplify.

## Rule 3 — Surgical Changes
Touch only what you must. Clean up only your own mess.
Don't "improve" adjacent code, comments, or formatting.
Don't refactor what isn't broken. Match existing style.

## Rule 4 — Goal-Driven Execution
Define success criteria. Loop until verified.
Don't follow steps. Define success and iterate.
Strong success criteria let you loop independently.

## Rule 5 — Use the model only for judgment calls
Use me for: classification, drafting, summarization, extraction.
Do NOT use me for: routing, retries, deterministic transforms.
If code can answer, code answers.

## Rule 6 — Token budgets are not advisory
Per-task: 4,000 tokens. Per-session: 30,000 tokens.
If approaching budget, summarize and start fresh.
Surface the breach. Do not silently overrun.

## Rule 7 — Surface conflicts, don't average them
If two patterns contradict, pick one (more recent / more tested).
Explain why. Flag the other for cleanup.
Don't blend conflicting patterns.

## Rule 8 — Read before you write
Before adding code, read exports, immediate callers, shared utilities.
"Looks orthogonal" is dangerous. If unsure why code is structured a way, ask.

## Rule 9 — Tests verify intent, not just behavior
Tests must encode WHY behavior matters, not just WHAT it does.
A test that can't fail when business logic changes is wrong.

## Rule 10 — Checkpoint after every significant step
Summarize what was done, what's verified, what's left.
Don't continue from a state you can't describe back.
If you lose track, stop and restate.

## Rule 11 — Match the codebase's conventions, even if you disagree
Conformance > taste inside the codebase.
If you genuinely think a convention is harmful, surface it. Don't fork silently.

## Rule 12 — Fail loud
"Completed" is wrong if anything was skipped silently.
"Tests pass" is wrong if any were skipped.
Default to surfacing uncertainty, not hiding it.


## Commands

```bash
# Run
go run ./cmd/xgateway --env-file .env

# Test
go test ./...
go test -run TestName ./internal/rules/   # single test or package

# Lint
golangci-lint run
```

## Architecture

Entry: `cmd/xgateway/main.go` → `internal/app/app.go` → `internal/proxy`

### Provider System (`internal/rules/`)

Two implementations of `Provider` interface (`List`, `GetByInboundPath`):
- **FileProvider** — loads `config.Config` at startup, resolves rules with glob matching
- **HttpProvider** — per-request `GET /proxy/resolve/{path}` call to control-api with `X-Api-Key` auth; 404 = no rule

Provider is selected by `CONFIG_PROVIDER` env var (`file` | `http`). Config structs live in two packages:
- `config/` — `GatewayConfig`, env-var loading (uses `caarlos0/env`), provider validation
- `internal/config/` — YAML/JSON rule schema, payment method defaults, `Validate()`

### Proxy (`internal/proxy/`)

Three-file structure:
- **`router.go`** — the core: Gin router, middleware chain, route/facilitator caching, entry building
- **`payment.go`** — x402 and MPP middleware wrappers, settlement, amount parsing
- **`server.go`** — server construction, upstream reverse proxy, auth header injection
- **`http.go`** — `ginAdapter` bridging Gin↔x402 interface, `responseCapture`, public URL resolution
- **`log_context.go`** — request state accumulator, retry fingerprinting, logger dispatch

**Middleware chain** (applied per request):
1. `loggingMiddleware` — creates `logContext`, detects payment retries via `method+path+IP` fingerprint
2. `resolve` — looks up rule via provider, builds entry (reverse proxy + payment servers), caches by path
3. payment middleware — if `PaymentChannels` present: MPP if `Authorization` header present, else x402
4. `proxyUpstream` — forwards to upstream, measures latency, finalises `logContext`

**Caching:**
- `routeCache` (`sync.RWMutex` map) — path → entry, built once on first request
- `facilCache` — x402 facilitator URL → client
- `pendingLogs` (`sync.Map`) — fingerprint → pending log, 10-min TTL for correlating 402 retries

### Logger (`internal/logger/`)

Fire-and-forget: `Dispatch(create, updates)` spawns a goroutine and POSTs to control-api (`/api/v1/request-logs`). No-op when `baseURL` is empty.

### Payment Protocols

- **x402**: `exact` (fixed price) and `upto` (upstream-response-based settlement) schemes. Uses `x402-foundation/x402/go` library. Facilitator URL, network (CAIP-2), merchant address, and asset (CAIP-19) are required.
- **MPP**: `tempo` and `stripe` methods; `charge` and `session` schemes. Uses `tempoxyz/mpp-go` library.

Protocol is selected per `PaymentChannel.Protocol`; a single route can have multiple channels (x402 + MPP).

## Key Types

```go
// internal/rules/provider.go
type Rule struct {
    InboundPath, OutboundPath, Target string
    Price string
    Free bool
    PaymentChannels []*PaymentChannel
    AuthHeaderName, AuthHeaderValue string
    AllowUnmatched bool
    // ...
}

type PaymentChannel struct {
    Protocol string            // "x402" | "mpp"
    Scheme string              // "exact"|"upto" (x402) or "charge"|"session" (mpp)
    Method string              // "tempo"|"stripe" (mpp only)
    ChannelConfig map[string]string // facilitator_url, network, merchant, rpc_url, asset, secret_key
}
```

## Environment Variables

| Var | Default | Notes |
|---|---|---|
| `CONFIG_PROVIDER` | `file` | `file` or `http` |
| `CONFIG_FILE` | `config.yaml` | Path to YAML/JSON config |
| `PORT` | `8080` | Listen port |
| `GIN_MODE` | `release` | Gin logging |
| `CONTROL_API_URL` | — | Required for HTTP provider |
| `INTERNAL_API_KEY` | — | Required for HTTP provider |
| `PUBLIC_URL` | — | Override public-facing URL in 402 responses |

## Testing Patterns

Tests use `t.Setenv()` for env var isolation. No mocks — structs are constructed directly. File provider tests verify rule loading, glob path matching, and that `List()` returns clones (not references). Config tests cover validation error messages and default application.

## Constraints

- Go 1.26; `golangci-lint` enforced; max line length 140
- No `logrus`, no `pkg/errors`
- Never hand-edit files in `internal/storage/postgres/generated/` (sqlc output — not applicable here but noted for the monorepo)
- The `internal/config/` schema drives both file-based loading and file provider's `fromConfigRule()` transformer
