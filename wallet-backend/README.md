# wallet-backend

A base, brand-agnostic wallet backend for **Base** (Coinbase's OP-Stack
Ethereum L2). See [`PLAN.md`](./PLAN.md) for the full architecture
rationale - this README is the practical "how do I run it" companion.

Wallets in this template are **non-custodial**: the server only ever handles
a user's EVM *address*. Every action that touches a user's own account (a
payment, an approval, a swap) follows a **build → sign → submit** flow: the
server builds an unsigned transaction with everything needed to sign it with
zero further network access, the client signs it offline with a key only it
holds, and the server submits the signed result. The server never asks for,
stores, or signs with a user's private key. See `PLAN.md` §3 for why this
matters for an app that needs to work offline.

## Quick start

```bash
go mod download
cp .env.example .env       # defaults work out of the box: SQLite + Base Sepolia + console mail/SMS
go run .                   # serves on :8080
```

No Postgres/Redis account needed to boot - `DB_TYPE=sqlite` and
`ENABLE_CACHING=false` are the defaults, and outbound mail/SMS/push log to
the console instead of sending anything. `BASE_RPC_URL`/`BASE_CHAIN_ID`
default to Base Sepolia (the public testnet), never mainnet.

To run against Postgres + Redis locally:

```bash
docker compose up
```

## Tech stack

- Go, [Gin](https://github.com/gin-gonic/gin) for HTTP
- [GORM](https://gorm.io) over Postgres (production) or SQLite (dev/tests)
- [go-ethereum](https://github.com/ethereum/go-ethereum) for all Base/EVM interaction - Base speaks standard Ethereum JSON-RPC, so no chain-specific SDK is needed
- [siwe-go](https://github.com/spruceid/siwe-go) for Sign-In With Ethereum (EIP-4361)
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
├── network/        Base/EVM integration - the only package importing go-ethereum
├── middleware/      CORS, SIWE-session + admin JWT auth
├── cryptoutil/       secp256k1 key derivation, AES-GCM, bcrypt, hashing
├── apperrors/        GenericError + typed constructors
├── validators/       format validators (EIP-55 address checks, etc.)
├── notify/           Mailer / SMSProvider / PushProvider + default impls
├── storage/          Blob interface + local-disk impl
├── kyc/              Provider interface + ManualKYCProvider stub
├── fiat/             Processor interface (no default impl - see doc comment)
├── rates/            Provider interface + static/fixture impl
├── alerting/         Notifier interface + Discord webhook impl
└── components/
    ├── root/          health check reporting the configured chain ID
    ├── auth/           SIWE nonce issuance + verification -> session JWT
    ├── users/          registration, profile, security questions/recovery
    ├── assets/         curated-token catalog, balance lookup, allowance build/submit
    ├── payments/       build/submit native or ERC-20 transfer, payment history
    ├── swaps/          generic, router-address-configurable DEX call builder
    ├── announcements/  in-app announcements (public read, JWT-admin write)
    └── callbacks/      generic webhook receiver stub
```

## Auth

- **Primary API** (`/v1/users`, `/v1/assets`, `/v1/payments`, `/v1/swaps`):
  Sign-In With Ethereum (SIWE, EIP-4361) exchanged for a session JWT.

  1. `GET /v1/auth/nonce` → `{"nonce": "..."}`
  2. Client builds a SIWE message (domain must match `SIWE_DOMAIN`, chain ID
     must match `BASE_CHAIN_ID`) embedding that nonce, and signs it with the
     wallet's `personal_sign`.
  3. `POST /v1/auth/verify` with `{"message": "...", "signature": "0x..."}`
     → `{"address": "0x...", "token": "<JWT>"}`
  4. Send `Authorization: Bearer <token>` on subsequent requests.

  See `internal/components/auth/services` for the verification flow and
  `internal/middleware/jwt_auth.go` for the session-JWT mechanics.

- **Admin surface** (`POST /v1/admin/announcements`): a separate-audience
  Bearer JWT issued via `middleware.IssueToken(..., middleware.AudienceAdmin, ...)`
  (wire up a real admin login flow before shipping this to production - none
  is included in the base template). A wallet-session token can never be
  used against an admin route or vice versa - see the `Audience*` constants
  in `internal/middleware/jwt_auth.go`.

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

`internal/components/swaps` doesn't hardcode a DEX: it ABI-encodes whatever
router address, ABI fragment, and method a caller configures (see
`internal/network/abi.go`), so it works unmodified against Uniswap V3's
`SwapRouter02`, Aerodrome, or any other router deployed on Base.

## Testing

```bash
make test     # go test ./... -race -coverprofile=coverage.out
make ci       # tidy-check, build, vet, lint, test - same as the GitHub Actions workflow
```

## Deployment

`Dockerfile` builds a static binary into a minimal Alpine image (SQLite
support is pure-Go via `glebarez/sqlite`, and go-ethereum's signature
verification has a pure-Go fallback, so `CGO_ENABLED=0` works). CI
(`.github/workflows/ci.yml`) intentionally stops at build/vet/lint/test -
add a deploy job once you've picked a registry and host; see the comment at
the bottom of that file for the shape the upstream project used.
