# xgateway

HTTP reverse proxy that enforces [x402](https://www.x402.org/) payment rules before forwarding requests to an upstream service.

A request without a valid payment proof is answered with `HTTP 402` and the payment requirements. Once the client retries with a verified proof, the gateway settles the payment and proxies the request upstream.

xgateway is used standalone or as the proxy component of the [xpaywall](https://github.com/cp0x-org/xpaywall) stack (where it is consumed as a Git submodule).

> **Payment protocol support:** Only **x402** (`exact`) is shipped in the current build. Other schemes and protocols (e.g. `upto`, MPP, Tempo) are on the roadmap but not yet available.

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
