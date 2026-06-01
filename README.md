# xgateway

HTTP reverse proxy that enforces [x402](https://www.x402.org/) payment rules before forwarding requests to an upstream service.

A request without a valid payment proof is answered with `HTTP 402` and the payment requirements. Once the client retries with a verified proof, the gateway settles the payment and proxies the request upstream.

xgateway is used standalone or as the proxy component of the [xpaywall](https://github.com/cp0x-org/xpaywall) stack (where it is consumed as a Git submodule).

> **Payment protocol support:** Only **x402** (`exact`) is shipped in the current build. Other schemes and protocols (e.g. `upto`, MPP, Tempo) are on the roadmap but not yet available.

---

## Why xgateway

Monetising an HTTP API usually means bolting on a billing system, issuing API keys, running a subscription database, and writing the per-request accounting code yourself. xgateway replaces all of that with a single drop-in proxy:

- **Charge per request, not per month.** Every call is a discrete on-chain payment — there are no accounts, no invoices, no subscriptions to cancel. Clients pay only for what they use, you get paid the moment the call is made.
- **No client signup, no keys to manage.** A caller does not need to register with you. They just hit the URL — xgateway answers `HTTP 402` with the payment requirements, the client pays on-chain and retries, and the request goes through. The contract between you and the caller is the route price.
- **Drop it in front of any API.** Your upstream service does not need to know xgateway exists. Point the gateway at `http://your-api:port`, define which paths cost what, and the gateway handles payment verification, settlement, and proxying. No code change in the upstream.
- **Per-route pricing and free paths.** Each route has its own price. Mark health checks or auth endpoints as `free: true` and they bypass payment entirely.
- **Bring-your-own facilitator.** x402 settlement is delegated to a facilitator URL of your choice — run your own or use a public one. The gateway is not tied to any single provider.
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

Copy [`config.example.yaml`](./config.example.yaml) to `config.yaml` and edit. The config has two top-level sections:

```yaml
# 1. x402 payment channels (one entry per facilitator / scheme).
x402:
  - name: "x402-base-exact"
    facilitator_url: "https://x402.dexter.cash"
    network: "eip155:84532"                  # CAIP-2
    scheme: "exact"                          # exact | upto
    merchant: "0xEb6ae6fA22D307Eae06BE0862087FdFFdD25Bab4"
    asset: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
    sync_facilitator_on_start: true
    timeout_seconds: 30

# 2. Upstream target and the routes the gateway protects.
outbound:
  target: "http://localhost:4021"
  allow_unmatched: false                     # true = proxy unmatched paths without payment
  auth_header:
    enable: true
    name: "Authorization"
    value: "Bearer YOUR_UPSTREAM_ACCESS_TOKEN"
  rules:
    - name: "weather"
      path: "/weather"
      price: "$0.10"
      payment_methods: ["x402-base-exact"]   # references x402[].name
    - name: "metered-api"
      path: "/api/metered/*"
      price: "$0.10"                         # maximum authorized amount for `upto` scheme
    - name: "free-endpoint"
      path: "/free-endpoint"
      free: true
```

Route paths support `*` glob suffixes. A rule with no `payment_methods` accepts every channel defined in `x402:`.

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
