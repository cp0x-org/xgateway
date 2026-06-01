# Contributing to xgateway

xgateway is the payment-enforcing HTTP reverse proxy used by the [xpaywall](https://github.com/cp0x-org/xpaywall) stack. It is published as its own repository and consumed from xpaywall as a Git submodule. This guide covers contributing **directly to xgateway**.

## Scope of this repository

xgateway is a single Go service:

- `cmd/xgateway/` — entry point and CLI flags
- `internal/proxy/` — Gin server, payment middleware, upstream proxying, log dispatch
- `internal/rules/` — `file` and `http` rule providers
- `internal/config/` — YAML/JSON rule schema and validation
- `internal/logger/` — fire-and-forget log shipper to control-api
- `config/` — env-var loading

It depends on:

- [x402-foundation/x402/go](https://github.com/x402-foundation/x402) for x402 protocol primitives
- A control-api instance when running in `http` mode (provided by the xpaywall stack)

## Prerequisites

- Go 1.26+
- `golangci-lint` on `$PATH`
- Docker (for the `Dockerfile` build target)

## Building and running

```bash
go run ./cmd/xgateway --env-file .env
```

By default the gateway loads `config.yaml` from the working directory and listens on `:8080`. Set `CONFIG_PROVIDER=http` plus `CONTROL_API_URL` and `INTERNAL_API_KEY` to fetch rules from a remote control plane.

See [`README.md`](README.md) for the full env-var reference and a sample `config.yaml`.

## Pull request checklist

Every PR must pass:

```bash
go test ./...
golangci-lint run
```

Before opening a PR:

- Run the commands above on a clean checkout.
- Keep the diff focused. Don't include unrelated refactors or formatting changes.
- Match the existing code style:
  - Max line length 140
  - No `logrus`, no `pkg/errors`
  - Standard library `log/slog` for logging
- Add or update tests when you change behaviour. Tests should encode **why** behaviour matters, not just what it does.
- Update [`README.md`](README.md) and [`config.example.yaml`](config.example.yaml) when you change user-visible configuration.

## Testing patterns

- Use `t.Setenv()` for env-var isolation. Do not mutate `os.Environ` directly.
- Avoid mocks where a real struct will do. Provider tests construct config structs in-line.
- File-provider tests should verify that `List()` returns clones, not references — accidental shared state has bitten us before.

## Payment protocol changes

If you touch x402 or add a new protocol:

- Update `internal/config/config.go` and the schema validation.
- Add a `ResolvedPaymentMethod` accessor and wire the middleware in `internal/proxy/payment.go`.
- Add tests covering both 402 generation and successful settlement.

> Note: MPP / Tempo support is currently commented out and not shipped. If you re-enable it, ensure every commented block in `internal/config/config.go` and the proxy middleware is consistently restored.

## Commit messages

Follow Conventional Commits:

```
feat(proxy): describe change
fix(rules): describe fix
docs: describe doc change
```

## Versioning and the xpaywall submodule

xgateway is consumed as a submodule in `cp0x-org/xpaywall` at the path `xgateway/`. After your PR is merged here:

1. Tag a release in this repo if the change is API- or config-visible.
2. Open a PR in `xpaywall` that bumps the submodule pointer:

   ```bash
   cd xgateway
   git pull origin main
   cd ..
   git add xgateway
   git commit -m "chore(xgateway): bump submodule to <sha>"
   ```

## Reporting bugs and proposing features

Open an issue describing:

- What you expected to happen
- What actually happened (status code, log output, response body)
- Minimal reproduction: a `config.yaml` snippet and the request that triggered the issue
- Versions: `git rev-parse HEAD`, Go version, and (if relevant) the control-api version

## Security

Do **not** open a public issue for security reports. Follow the disclosure process documented in the parent xpaywall repository (`docs/09-security.md`).

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
