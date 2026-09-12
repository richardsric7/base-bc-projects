# wallet-backend

A base, brand-agnostic Stellar blockchain wallet backend. See
[`PLAN.md`](./PLAN.md) for the full architecture rationale - this README is
the practical "how do I run it" companion.

Wallets in this template are **non-custodial**: the server only ever handles
a user's Stellar *public* key. Every action that touches a user's own
account (a payment, a trustline, a swap) follows a **build → sign → submit**
flow: the server builds an unsigned transaction, the client signs it with a
key only it holds, and the server submits the signed result. The server
never asks for, stores, or signs with a user's secret key.

## Quick start

```bash
go mod download
cp .env.example .env       # defaults work out of the box: SQLite + console mail/SMS
go run .                   # serves on :8080
```

No Postgres/Redis/Stellar account needed to boot - `DB_TYPE=sqlite` and
`ENABLE_CACHING=false` are the defaults, and outbound mail/SMS/push log to
the console instead of sending anything.

To run against Postgres + Redis locally:

```bash
docker compose up
```

## Tech stack

- Go, [Gin](https://github.com/gin-gonic/gin) for HTTP
- [GORM](https://gorm.io) over Postgres (production) or SQLite (dev/tests)
- [go-stellar-sdk](https://github.com/stellar/go-stellar-sdk) for all Stellar/Horizon interaction
- Redis for optional response/data caching

## Project layout

Each component under `internal/components/` is a vertical slice
(`controllers/` → `services/` → `models/`) that registers its own routes via
an `Init(router, gc)` function called from `main.go`. `sharedconfig.GlobalConfig`
is the single dependency-injection struct threaded through every component -
see `internal/sharedconfig/config.go`.

```
internal/
├── sharedconfig/   GlobalConfig (DI struct) + Env loader
├── db/             OpenDB (postgres/sqlite), MigrateDB
├── cache/          Cache interface + Redis impl + no-op
├── network/        Stellar/Horizon integration - the only package importing txnbuild/horizonclient
├── middleware/      CORS, Stellar-signature auth, JWT admin auth
├── cryptoutil/       keypair derivation, AES-GCM, bcrypt, hashing
├── apperrors/        GenericError + typed constructors
├── validators/       format validators
├── notify/           Mailer / SMSProvider / PushProvider + default impls
├── storage/          Blob interface + local-disk impl
├── kyc/              Provider interface + ManualKYCProvider stub
├── fiat/             Processor interface (no default impl - see doc comment)
├── rates/            Provider interface + static/fixture impl
├── alerting/         Notifier interface + Discord webhook impl
└── components/
    ├── root/          health check, SEP-1 stellar.toml
    ├── users/         registration, profile, security questions/recovery
    ├── assets/        curated-asset catalog, trustline build/submit
    ├── payments/      build/submit payment, payment history
    ├── swaps/         build/submit path-payment swap
    ├── announcements/ in-app announcements (public read, JWT-admin write)
    └── callbacks/     generic webhook receiver stub
```

## Auth

- **Primary API** (`/v1/users`, `/v1/assets`, `/v1/payments`, `/v1/swaps`,
  the write side of `/v1/users/security-answers`): Stellar-signature auth.
  The client signs `<publicKey><unixTimestamp>` with its wallet's secret key
  and sends:

  ```
  X-Public-Key: G...
  X-Timestamp: 1732550400
  X-Signature: <base64 ed25519 signature>
  ```

  See `internal/middleware/signature_auth.go`.

- **Admin surface** (`POST /v1/admin/announcements`): a Bearer JWT, issued
  via `middleware.IssueAdminToken` (wire up a real admin login flow before
  shipping this to production - none is included in the base template).

## Adding a real integration

The `notify`, `storage`, `kyc`, `fiat`, `rates`, and `alerting` packages are
small interfaces with a free/local default implementation (console loggers,
local disk, a manual-review stub, static rates, a no-op notifier). Swap one
out by implementing its interface and wiring the new type into
`main.go`'s `GlobalConfig` construction - no component code changes.

`internal/fiat` ships no default implementation: fiat payment rails are too
jurisdiction-specific to fake usefully. Its doc comment lays out the
recommended "build the on-chain leg → charge → submit on webhook" shape for
whichever processor you plug in.

## Testing

```bash
make test     # go test ./... -race -coverprofile=coverage.out
make ci       # tidy-check, build, vet, lint, test - same as the GitHub Actions workflow
```

## Deployment

`Dockerfile` builds a static binary into a minimal Alpine image (SQLite
support is pure-Go via `glebarez/sqlite`, so `CGO_ENABLED=0` works). CI
(`.github/workflows/ci.yml`) intentionally stops at build/vet/lint/test -
add a deploy job once you've picked a registry and host; see the comment at
the bottom of that file for the shape the upstream project used.
