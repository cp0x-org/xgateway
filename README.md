# xgateway

HTTP reverse proxy that enforces [x402](https://www.x402.org/) and [MPP](https://mpp.dev/) (Machine Payments Protocol) payment rules before forwarding requests to an upstream service.

A request without a valid payment proof is answered with `HTTP 402` and the payment requirements. Once the client retries with a verified proof, the gateway settles the payment and proxies the request upstream.

xgateway is used standalone or as the proxy component of the [xpaywall](https://github.com/cp0x-org/xpaywall) stack (where it is consumed as a Git submodule).

> **Payment protocol support (current build):**
> - **x402** — `exact` (fixed price) and `upto` (settle by upstream-reported usage).
> - **MPP** — Tempo `charge` (one-time on-chain charge) only. The `session` scheme and the `stripe` method are recognised in config but rejected during validation; they are not yet available.
>
> A single route may offer both protocols. The gateway uses MPP when the client sends an MPP `Authorization` header (or when MPP is the only channel on the route), and x402 otherwise.

---

## Why xgateway

Monetising an HTTP API usually means bolting on a billing system, issuing API keys, running a subscription database, and writing the per-request accounting code yourself. xgateway replaces all of that with a single drop-in proxy:

- **Charge per request, not per month.** Every call is a discrete on-chain payment — there are no accounts, no invoices, no subscriptions to cancel. Clients pay only for what they use, you get paid the moment the call is made.
- **No client signup, no keys to manage.** A caller does not need to register with you. They just hit the URL — xgateway answers `HTTP 402` with the payment requirements, the client pays on-chain and retries, and the request goes through. The contract between you and the caller is the route price.
- **Drop it in front of any API.** Your upstream service does not need to know xgateway exists. Point the gateway at `http://your-api:port`, define which paths cost what, and the gateway handles payment verification, settlement, and proxying. No code change in the upstream.
- **Per-route pricing and free paths.** Each route has its own price. Mark health checks or auth endpoints as `free: true` and they bypass payment entirely.
- **Multiple payment rails per route.** Each route can accept x402, MPP (Tempo), or both. x402 settlement is delegated to a facilitator URL of your choice — run your own or use a public one; MPP charges are verified against a Tempo RPC endpoint. The gateway is not tied to any single provider.
- **Self-hosted, MIT-licensed, Go-native.** One static binary, one config file (or one control-api URL). Run it in Docker, on bare metal, behind your existing reverse proxy — wherever fits.

### Where it sits

```
Client ──HTTP──▶ xgateway (handle payments) ──HTTP──▶ Upstream API
                    │
                    ├── (HTTP mode) ──▶ control-api ──▶ PostgreSQL
                    │
                    └── (file mode) ──▶ config.yaml on disk
```

Every request follows the top row. The bottom row is how xgateway gets its rules — that part happens at different cadences depending on the mode.


### Two ways to configure routes

xgateway loads its routes from one of two providers, selected at boot by the `CONFIG_PROVIDER` env var:

| Provider | When to use |
|---|---|
| **`file`** (default) | A single project, a static list of routes, no admin UI needed. Routes and payment channels live in a local `config.yaml`. Reload requires a restart. |
| **`http`** | Multiple projects, routes that change at runtime, or a central admin panel. The gateway calls a remote control API (`CONTROL_API_URL`) per request to resolve the rule for the inbound path. Live changes apply on the next request — no restart. |

The `http` provider is what the full [xpaywall](https://github.com/cp0x-org/xpaywall) stack uses: a control-api service holds routes, projects, payment configuration and request logs in PostgreSQL, an admin panel manages them, and xgateway just enforces payment in front of the upstream. You can also point the `http` provider at your own backend that implements `GET /proxy/resolve/*path` if you do not want to run control-api.

📖 **Full proxy overview and architecture:** <http://xpaywall.cp0x.com/docs/xgateway/overview>

---

## Run

```bash
go run ./cmd/xgateway --env-file .env
```

By default the gateway loads configuration from `config.yaml` in the working directory and listens on `:8080`.

## Configuration

Two configuration sources are supported, selected by the `CONFIG_PROVIDER` env var:

| Provider | Description |
|---|---|
| `file` (default) | Load routes and payment channels from a local YAML file. |
| `http` | Fetch routes per-request from a remote control API (`CONTROL_API_URL`) authenticated with `INTERNAL_API_KEY`. |

### Environment variables

| Var | Default | Notes |
|---|---|---|
| `CONFIG_PROVIDER` | `file` | `file` or `http`. |
| `CONFIG_FILE` | `config.yaml` | Path to YAML config. Used when `CONFIG_PROVIDER=file`. |
| `PORT` | `8080` | Listen port. |
| `GIN_MODE` | `release` | Gin mode (`debug` / `release`). |
| `DEBUG` | `false` | Verbose proxy logging. |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR`. |
| `CONTROL_API_URL` | — | Required when `CONFIG_PROVIDER=http`. |
| `INTERNAL_API_KEY` | — | Required when `CONFIG_PROVIDER=http`; sent as `X-Api-Key`. |
| `PUBLIC_URL` | — | Override the public-facing URL injected into 402 responses. |

`--env-file <path>` loads variables from a dotenv file before parsing. `.env` in the working directory is loaded automatically when present.

### File config

Copy [`config.dev.yaml`](./config.dev.yaml) to `config.yaml` and edit. The config has three top-level sections — `x402` and `mpp` declare named payment channels, and `outbound` defines the upstream target and the routes that reference those channels.

```yaml
# 1. x402 payment channels (one entry per facilitator / scheme).
x402:
  - name: "x402-base-exact"
    facilitator_url: "https://x402.dexter.cash"
    network: "eip155:84532"                  # Base Sepolia (CAIP-2)
    scheme: "exact"                          # exact | upto
    merchant: "0xEb6ae6fA22D307Eae06BE0862087FdFFdD25Bab4"
    asset: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
    decimals: 6
    sync_facilitator_on_start: true
    timeout_seconds: 30

# 2. MPP payment channels. Only the Tempo "charge" scheme is supported.
mpp:
  - name: "mpp-tempo-charge"
    method: "tempo"                          # only "tempo" is supported
    scheme: "charge"                         # only "charge" is supported ("session" is rejected)
    rpc_url: "https://rpc.moderato.tempo.xyz"  # Tempo JSON-RPC endpoint
    merchant: "0xb7Ac41661288791225d66643a7d952e551FC7868"
    asset: "0x20c0000000000000000000000000000000000000"  # pathUSD (Tempo stablecoin)
    decimals: 6                              # base-unit precision of route `price`
    secret_key: "xgateway-dev-secret"        # HMAC key signing 402 challenges
    timeout_seconds: 30

# 3. Upstream target and the routes the gateway protects.
outbound:
  target: "http://localhost:4021"
  allow_unmatched: false                     # true = proxy unmatched paths without payment
  auth_header:
    enable: true
    name: "Authorization"
    value: "Bearer YOUR_UPSTREAM_ACCESS_TOKEN"
  rules:
    - name: "weather"                        # accepts both rails: MPP when the client
      path: "/weather"                       # sends an MPP Authorization header, else x402
      price: "100000"                        # $0.10 in base units (6 decimals)
      payment_methods: ["x402-base-exact", "mpp-tempo-charge"]
    - name: "metered-api"
      path: "/api/metered/*"
      price: "100000"                        # maximum authorized amount for `upto` scheme
      payment_methods: ["x402-base-exact"]
    - name: "http-endpoint"                  # MPP-only route
      path: "/http-endpoint"
      price: "10000"                         # $0.01 in base units (6 decimals)
      payment_methods: ["mpp-tempo-charge"]
    - name: "free-endpoint"
      path: "/free-endpoint"
      free: true
```

Route paths support `*` glob suffixes. `payment_methods` references channels by their `name` in the `x402:` and `mpp:` sections; a rule with no `payment_methods` accepts every configured channel.

`price` is expressed in the asset's **base units** (smallest indivisible unit), scaled by the channel's `decimals` — e.g. with `decimals: 6`, `"100000"` is `$0.10` and `"1000"` is `$0.001`. MPP requires this numeric form; do not use a `"$0.10"` dollar string.

Paid x402 routes are also advertised via the x402 **Bazaar** discovery extension. A minimal declaration is auto-generated for each paid x402 rule; add a `bazaar:` block to a rule to publish a richer contract (input/output examples and JSON schemas). Bazaar applies to x402 only — MPP routes ignore it. See [`config.dev.yaml`](./config.dev.yaml) for a fully annotated `bazaar:` example.

## Docker

```bash
docker build -t xgateway .
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/app/config.yaml:ro" \
  -e CONFIG_FILE=/app/config.yaml \
  xgateway
```

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the dev setup, PR checklist, and how changes flow back into the xpaywall stack.

## License

Released under the [MIT License](LICENSE).
