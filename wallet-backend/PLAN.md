# wallet-backend — Base Blockchain Wallet Backend

## Purpose

A reusable, brand-agnostic starting point for a Stellar-based blockchain
wallet backend, derived from `trovo-wallet-monorepo/backend` (the "Trovo
Wallet API"). It keeps the general-purpose architecture and blockchain
integration patterns that any wallet product needs, and strips out (or
replaces with pluggable stubs) everything that is specific to Trovo's
business: real-world-asset tokenization, its partner/merchant API, and its
hard-wired third-party vendors.

Source of truth for "what exists today": `trovo-wallet-monorepo/backend`
(Go, Gin, GORM/Postgres, Redis, `github.com/stellar/go`). This plan was
written after a full inventory of that codebase (`main.go`, every
`internal/` package, and its `docs/`, `TOKENIZATION_PLAN.md`,
`TOKENIZED_ASSET_PURCHASE_BY_FIAT.md`, `issue-internal-balance-on-demand.md`).

## 1. What carries over as-is (the reusable "base")

These are generic to any Stellar wallet product and should be ported with
only cosmetic/naming changes:

| Area | Source | Notes |
|---|---|---|
| Boot sequence & DI pattern | `main.go` | Load env → open DB(s) → migrate → init cache/blockchain client → build one `GlobalConfig` struct → call each component's `Init(router, gc)`. Keep the manual-service-locator style; it's simple and traceable. |
| Layered component structure | `internal/components/<name>/{controllers,services,models,db?}` | Vertical slices per feature, each owning its routes via `Init()`. This is the template's core convention. |
| Stellar integration layer | `internal/network` | Horizon client construction, `AccountDetail`, transaction build/sign/submit helpers, trustline (`ChangeTrust`) and asset (`CreditAsset`/`NativeAsset`) helpers, Horizon-error classification. |
| Wallet auth model | `internal/middleware` (signature auth) | Non-custodial: the user's Stellar keypair *is* the login credential. Client signs `publicKey+timestamp`; server verifies with `keypair.Verify`. No server-side password for the main API. |
| Admin/staff auth | `internal/middleware` (JWT) | Separate, simpler surface for an internal admin panel. |
| Error shape | `internal/errors` | Typed errors → consistent `{error, message, data}` JSON. |
| DB migration harness | `internal/db` | Central `MigrateDB` that `AutoMigrate`s every component's models; supports Postgres, with SQLite for local dev. |
| Cache abstraction | `internal/cache` | Redis-backed, no-op when disabled — every caller stays cache-optional. |
| Deterministic keypair derivation | `internal/blockchainalgofuncs` | Real pattern for server-controlled signer roles (a recovery co-signer, a market-maker account) derived from `SHA256(salt + role + identifier)`. **Rename to `cryptoutil`** — the original name reads as "Algorand functions" and has caused confusion; it is pure crypto/hash/AES/bcrypt helpers with zero Algorand involvement. |
| Validators | `internal/validators` | Format checks (email, username, asset code, Stellar public key). |
| Core payment/swap/trustline flows | `internal/components/{payments,swaps,assets}` | Build-unsigned-tx → client signs → submit-signed-tx is the reusable pattern for every wallet-mutating action, since wallets are non-custodial. |
| Announcements & root/health | `internal/components/{announcements,root}` | Simple, product-agnostic. |

## 2. What gets replaced with a generic/pluggable equivalent

The original hard-wires specific paid vendors. The base template instead
defines a small interface per integration point and ships one free/local
default implementation, so a real project swaps in a provider without
touching business logic.

| Capability | Original | Base template |
|---|---|---|
| Email | Mailgun SDK directly in `internal/mail` | `notify.Mailer` interface; ship `SMTPMailer` (works with any SMTP server, including Mailtrap for dev) and a `ConsoleMailer` default. |
| SMS | Infobip + Termii, provider chosen by phone-prefix DB lookup | `notify.SMSProvider` interface; ship a `ConsoleSMSProvider` default. Document the phone-prefix-routing pattern as an optional extension, not baked in. |
| Push notifications / file storage | Firebase Cloud Messaging + Firebase Storage, **hard `log.Fatalln` on boot if unavailable** | `notify.PushProvider` + `storage.Blob` interfaces, both optional/no-op by default so the service boots without any Firebase project. Firebase becomes one pluggable implementation, not a boot dependency. |
| KYC | Sumsub + Doja, deeply integrated into `users` | `kyc.Provider` interface (`StartVerification`, `HandleWebhook`, `GetStatus`); ship a `ManualKYCProvider` stub that just flips a status field, for teams that review documents by hand or haven't picked a vendor yet. |
| Fiat payments | Flutterwave, Stablerail on/off-ramp | `fiat.Processor` interface; no default implementation shipped (fiat rails are too jurisdiction-specific to fake usefully) — document the two-phase "build & pre-sign the Stellar leg, then trigger the fiat charge, then submit on webhook" pattern from `TOKENIZED_ASSET_PURCHASE_BY_FIAT.md` as the recommended shape for whoever plugs one in. |
| Crypto on/off-ramp partner (OneLiquidity) | Hard-coded integration + webhook | Generic `internal/components/callbacks` webhook receiver stub: logs the payload, documents where per-provider signature verification and dispatch go. |
| Ops alerting | Discord webhooks hard-coded in several files, env-overridable | Fully env-driven `alerting.Notifier` interface (Discord webhook implementation kept as the default since it's free and simple), never a literal URL in source. |
| Exchange rates | `internal/components/rates`, live provider unspecified in inventory | `rates.Provider` interface; ship a static/fixture provider plus a cached-HTTP-fetch example. |

## 3. What gets dropped entirely

Business-specific to Trovo, not part of a "base" wallet:

- **Tokenization subsystem** (`tokenization.go`, ~6800 lines; `TokenizedAsset*` models; primary/secondary sale, early exit, payout-schedule engine, minting approvals). This is a real-world-asset securities platform bolted onto a wallet — out of scope for a generic base, but its two reusable *patterns* are written up in §5 below for later reuse.
- **Patron/membership subscriptions** (`Patron*` models/services) — a SaaS-tier feature unrelated to blockchain wallets.
- **Servicelinks partner API** (`/v1/trovo-api/...`, `/v1/servicelinks/...`, OAuth-like merchant delegation, stakeholder-document storage) — Trovo-specific B2B surface.
- **Stablerail-specific models/services** (BVN onboarding, NGN on/off-ramp polling) — one vendor's integration, not a generic pattern.
- Trovo-branded constants: asset codes (`TROV`, `XBN`, `BNR`), the `trovo-manager` admin route prefix, `trovo-logo.png`/`ht2.png`, hardcoded Discord webhook URLs.
- CockroachDB side-store (`OpenRoachDB`, `TrackedWallet`/`TrackedPublicKey`) — a payment-history mirror specific to Trovo's ops setup; a single Postgres instance is enough for a base template's `PaymentHistory` table.
- DigitalOcean Container Registry + Portainer-webhook deploy step in CI — replaced with a generic build/push-to-any-registry job left for the adopting team to point at their own infra.

## 4. Target module layout

```
wallet-backend/
├── main.go                       # boot sequence, GlobalConfig assembly, component Init() calls
├── go.mod
├── Dockerfile
├── docker-compose.yml            # postgres + redis, for local dev
├── .env.example
├── Makefile                      # build, vet, lint, test — CI parity
├── .golangci.yml
├── .github/workflows/ci.yml      # generic: build/vet/lint/test, no vendor-specific deploy step
├── README.md
├── internal/
│   ├── sharedconfig/             # GlobalConfig (DI struct) + Env loader
│   ├── db/                       # OpenDB (postgres/sqlite), MigrateDB
│   ├── cache/                    # Cache interface + Redis impl + no-op
│   ├── network/                  # Stellar/Horizon integration (the only package importing txnbuild/horizonclient)
│   ├── middleware/                # CORS, Stellar-signature auth, JWT admin auth
│   ├── cryptoutil/                # renamed from blockchainalgofuncs: keypair derivation, AES-GCM, bcrypt, hashing
│   ├── apperrors/                 # GenericError + typed constructors
│   ├── validators/                # format validators
│   ├── notify/                    # Mailer / SMSProvider / PushProvider interfaces + default impls
│   ├── kyc/                       # Provider interface + ManualKYCProvider stub
│   ├── fiat/                      # Processor interface (no default impl)
│   ├── rates/                     # Provider interface + static/fixture impl
│   └── components/
│       ├── root/                 # health check, SEP-1 stellar.toml
│       ├── users/                # registration, profile, security questions/recovery
│       ├── assets/                # curated-asset catalog, trustline build/submit
│       ├── payments/              # build/submit payment, payment history
│       ├── swaps/                 # build/submit path-payment swap
│       ├── announcements/         # in-app announcements + min app version
│       └── callbacks/             # generic webhook receiver stub
└── docs/                          # swagger output (generated, not hand-written)
```

Each component keeps the original's internal convention:
`controllers/` (HTTP layer, thin) → `services/` (business logic, unit-testable) →
`models/` (GORM structs) → optional `db/` when a component's migration list
should stay colocated with its models.

## 5. Blockchain design notes worth preserving

Even though tokenization itself is dropped, three Stellar patterns
documented in the original repo's planning docs are genuinely reusable and
worth keeping as documented techniques in this template's README/docs, so a
team that later builds asset-issuance or fiat-purchase features on top of
this base doesn't have to rediscover them:

1. **Ephemeral trustline** (`issue-internal-balance-on-demand.md`): open a
   trustline, use it within the same atomic transaction (mint + swap), then
   close it (`ChangeTrust Limit: "0"`) in that same transaction — so a user
   never holds a lingering trustline outside the app's control.
2. **Decoupled build/pay/submit for fiat purchases**
   (`TOKENIZED_ASSET_PURCHASE_BY_FIAT.md`): server pre-builds and partially
   signs the on-chain leg *before* the fiat charge starts; the client adds
   its signature; the fiat webhook triggers final submission — all three
   phases keyed by one client-supplied idempotency ID reused as the payment
   processor's reference.
3. **Config-driven settlement asset, not a hardcoded one**
   (`TOKENIZATION_PLAN.md`): any "quote currency" or intermediate
   settlement asset should be a per-deployment config value from day one,
   never a literal asset code baked into business logic.

## 6. Auth model for the base template

- **Primary API**: Stellar-signature auth (non-custodial) — ported as-is;
  this is the one piece of the original that *is* the generic pattern, not
  Trovo-specific.
- **Admin surface**: JWT auth, ported as-is but pointed at a small
  `internal/components/announcements` admin route as the only example
  (original's admin surface was mostly tokenization-admin, which is dropped).
- Security questions + email OTP for account recovery: ported as the
  reusable recovery pattern; Sumsub/Doja-specific KYC recovery hooks
  dropped.

## 7. Infra/CI

- `docker-compose.yml`: Postgres + Redis only (no CockroachDB).
- `Dockerfile`: same multi-stage Alpine build, minus Trovo image assets.
- CI: build, `go vet`, `golangci-lint`, `go test ./internal/...` — same
  shape as the original `Makefile`/`.golangci.yml`, kept minimal
  (`govet` only, documented path to add `errcheck`/`staticcheck`
  incrementally, exactly as the original does).
- Deploy step: intentionally omitted/templated — original's DO
  registry + Portainer webhook is infra-specific; leave a commented
  placeholder job instead of assuming any particular registry/host.

## 8. Suggested build order (when implementation is greenlit)

1. `internal/sharedconfig`, `internal/apperrors`, `internal/validators`, `internal/cryptoutil` — no external deps beyond stdlib/bcrypt, quick to get right.
2. `internal/db` + `internal/cache` — get Postgres/SQLite + Redis wired and migratable with zero components yet.
3. `internal/network` — the Stellar integration layer; this is the highest-risk/most-novel piece and should be validated against Stellar testnet early (`FundTestnetAccount` via Friendbot, a round-trip build→sign(offline)→submit test).
4. `internal/middleware` (signature auth) — depends on network's keypair usage patterns being settled.
5. `components/root`, `components/users` (registration + profile only) — first vertical slice end-to-end, proves the `Init(router, gc)` wiring.
6. `components/assets`, `components/payments`, `components/swaps` — the build/submit transaction pattern, repeated three times.
7. `components/announcements` (+ JWT admin auth) — proves the second auth scheme.
8. `components/callbacks` stub, `notify`/`kyc`/`fiat`/`rates` interfaces with default/no-op implementations — rounds out the pluggable-integration story without committing to any vendor.
9. Docker/CI/README/`.env.example` polish, `swag init` wiring for docs generation.

## 9. Open decisions for whoever implements this

- **SQLite driver**: original used a CGO SQLite-cipher fork; recommend a
  pure-Go driver (e.g. `glebarez/sqlite`) for the base template so `go build`
  needs no C toolchain — flag this as a deliberate deviation, not an oversight.
- **Stellar SDK**: `github.com/stellar/go` (used by the original) is now
  deprecated upstream in favor of `github.com/stellar/go-stellar-sdk`, which
  is API-compatible for the packages this template needs
  (`keypair`, `txnbuild`, `clients/horizonclient`, `network`, `protocols/horizon`).
  Recommend starting on the new module rather than inheriting deprecated debt.
- **JWT library**: original uses both `dgrijalva/jwt-go` (abandoned) and
  `golang-jwt/jwt` v3; standardize on `golang-jwt/jwt/v5`.
- How opinionated the default `notify`/`kyc`/`fiat` stubs should be vs. how
  bare — this plan assumes "boots and does something visible (logs/console)
  by default," never "silently no-ops with no trace."
