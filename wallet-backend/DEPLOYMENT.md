# Deploying wallet-backend

This covers taking `wallet-backend` from a checkout to a running
production (or staging) instance. See `PLAN.md` for the design and
`.env.example` for every configuration value with its own explanation.

## 1. Prerequisites

- **Go 1.26** to build (matches `.github/workflows/ci.yml` and
  `Dockerfile`), or just Docker if you're using the provided image.
- **PostgreSQL 14+** for anything beyond local dev. SQLite (`DB_TYPE=sqlite`)
  works for local dev only - a single-file database has no place serving
  concurrent production traffic.
- **Redis** only if `ENABLE_CACHING=true`. The service runs fine without
  it (caching is opt-in, not load-bearing).
- **A Base RPC endpoint.** Defaults to the public Base Sepolia RPC
  (`https://sepolia.base.org`) - fine for testing, not for production
  volume. Use a dedicated provider (Alchemy, Infura, QuickNode, or a
  self-hosted node) for anything real, and set `BASE_RPC_URL`/
  `BASE_CHAIN_ID` explicitly. **The chain ID default is deliberately
  Sepolia (testnet), not mainnet** - a forgotten env var must never
  silently point a fresh deployment at real funds. Confirm
  `BASE_CHAIN_ID=8453` yourself before going live.
- **wallet-payment-history-engine**, if you want indexed payment history
  beyond wallet-backend's own narrow submit-time records - see that
  project's own `DEPLOYMENT.md`. It's optional; wallet-backend runs fully
  without it.

## 2. Local development

```bash
cp .env.example .env   # every value has a working default except where noted
docker compose up      # postgres + redis + api, wired together
```

`docker-compose.yml` runs Postgres and Redis with health checks the `api`
service waits on, migrates the database automatically
(`DB_AUTOMIGRATE=true`), and exposes the API on `:8080`. Confirm it's up:

```bash
curl http://localhost:8080/
# {"service":"wallet-backend","status":"ok","chainId":84532}
```

Running without Docker: `DB_TYPE=sqlite` needs no external database at
all - `go run .` boots against a local SQLite file with every optional
integration (KYC, fiat, crypto rails, etc.) disabled by default.

## 3. Environment variables - what's required vs. optional

Every variable is documented inline in `.env.example`; this groups them
by consequence rather than repeating that file.

**Must be set explicitly for any non-dev environment** (all default to
an insecure, clearly-marked `dev-only-change-me` placeholder):

| Variable | Why |
|---|---|
| `JWT_SECRET` | Signs the admin-surface and servicelinks partner-login tokens. A leaked or guessed value lets an attacker forge those sessions. |
| `SIGNATURE_AUTH_TOLERANCE_SECONDS` | Bounds a signed request's allowed clock drift (PLAN.md §12.3) - the only replay defense in a scheme with no server-side session or nonce. Too wide weakens replay protection; too narrow breaks legitimate clients with imperfect clocks. Not a secret, but worth setting deliberately rather than leaving at its default. |
| `RECOVERY_AUTHORITY_SALT` | Derives the account-recovery attestation key. **Never rotate once any recovery attestation has been issued.** |
| `SAFE_DEPLOYER_KEY_SALT` | Derives the address that pays gas to deploy every Safe smart-contract wallet - primary wallets and sub-wallets/shared-access groups alike (PLAN.md §13.10 Phases 2-3) - fund that address, but unlike the salts above, rotating it is harmless: it has no ongoing authority over any wallet. |
| `FAUCET_KEY_SALT` | Derives the activation faucet's address - fund that address before enabling activation. |
| `CRYPTO_TREASURY_KEY_SALT` | Derives the crypto-deposit treasury address - fund it with every curated token before enabling deposits. |
| `MARKET_ESCROW_KEY_SALT` | Derives the market-making escrow address - rotating orphans standing maker approvals. |
| `TOKENIZATION_ISSUER_KEY_SALT` / `TOKENIZATION_DISTRIBUTION_KEY_SALT` | Derive per-asset issuer/treasury addresses - rotating either orphans on-chain contract ownership of every already-minted asset. |
| `PATRON_FEE_WALLET_SALT` | Derives the patron-subscription fee address. |

Treat every `*_KEY_SALT` and `JWT_SECRET`/`RECOVERY_AUTHORITY_SALT` as a
real secret: generate with a CSPRNG (`openssl rand -hex 32` is fine),
store in your platform's secret manager, never commit them, and
**never rotate a `*_KEY_SALT` that has already derived a live address**
without a deliberate, funded migration plan.

**Optional integrations** - each fails closed (returns a clear "not
configured" error, never a silent no-op that pretends to work) when its
credentials are blank: KYC (`SUMSUB_*`, `DOJA_SECRET_KEY`), fiat
(`FLUTTERWAVE_*`), Stablerail (`STABLERAIL_*`, also gated by
`STABLERAIL_ENABLED`), crypto deposits/withdrawals (`ONELIQUIDITY_*`),
geo-IP (`GEOIP_BASE_URL`), operational alerting (`DISCORD_WEBHOOK_URL`).
Enable only the ones your deployment actually uses.

**Database pool** (`DB_MAX_OPEN_CONNS`/`DB_MAX_IDLE_CONNS`/
`DB_CONN_MAX_LIFETIME_MINUTES`): the defaults (25/5/30) are a reasonable
starting point for a single-instance deployment; size up if you run
multiple replicas against the same Postgres and are watching connection
exhaustion.

## 4. Database migrations

`DB_AUTOMIGRATE=true` (the default) runs GORM's `AutoMigrate` for every
component's models at boot - additive schema changes (new tables/columns)
apply automatically on deploy. This is safe for this project's migration
style (no destructive auto-migrations are ever generated), but for a
larger production deployment where you want migrations reviewed and
applied as a distinct step rather than happening implicitly on every
pod restart, set `DB_AUTOMIGRATE=false` and run the binary once with it
`true` (or write a small migration-only entrypoint) as part of your
release process instead of at every boot.

## 5. Building and running

**Docker** (recommended - matches the CI-tested build):

```bash
docker build -t wallet-backend .
docker run -p 8080:8080 --env-file .env wallet-backend
```

**From source:**

```bash
go build -o wallet-backend .
./wallet-backend
```

The binary reads all configuration from the environment (via
`internal/sharedconfig`) - there is no config file.

## 6. CORS

`internal/middleware/cors.go` sets `Access-Control-Allow-Origin: *` by
default - permissive enough for local development against `wallet-web`
or any other client with zero configuration, but its own code comment
flags it as a template default to tighten before production. There is no
environment variable for this today; **before production, edit
`internal/middleware/cors.go` to allowlist your actual frontend
origin(s)** (e.g. `wallet-web`'s real deployed URL) instead of `*`,
rebuild, and redeploy. A wildcard origin combined with the
`Authorization` header this API accepts is a materially larger exposure
than a wildcard alone would be.

## 7. Health check and readiness

`GET /` returns `{"service", "status": "ok", "chainId"}` with no
authentication required - use it for your load balancer's or
orchestrator's health/readiness probe. It does not check database or
Base RPC connectivity, only that the process is up and knows its own
configuration; a deeper check (DB ping, RPC `eth_chainId`) is worth
adding to your monitoring separately if you need it, since a false
"ok" here while the database is down will otherwise only surface as
5xx errors on the next real request.

## 8. File storage

`STORAGE_DIR` (default `./data/uploads`) is where uploaded files
(tokenization documents, etc.) land on local disk. **This does not
survive a container restart or scale beyond one replica** unless backed
by a persistent volume (or you swap in an object-storage-backed
implementation) - mount `STORAGE_DIR` to durable storage before any
production traffic depends on uploaded documents persisting.

## 9. Order of deployment relative to the other ported projects

wallet-backend has no hard dependency on the other two projects and can
be deployed alone. If you're standing up the full stack:

1. **wallet-backend first** - it owns the `users`/`user_wallets`/
   `curated_tokens` tables the other two projects read.
2. **wallet-payment-history-engine second**, pointed at the *same*
   Postgres database (not a separate one - see its own `DEPLOYMENT.md`).
   It only reads wallet-backend's tables, never migrates or writes them.
3. **wallet-web last**, pointed at wallet-backend's public URL
   (`VITE_WALLET_BACKEND_URL`) and, once available, wallet-payment-
   history-engine's indexed history via wallet-backend's own
   `/v1/payments/history/:address` route.

## 10. Rollback

Since migrations here are additive-only `AutoMigrate` calls, rolling back
to a previous image version is generally safe - an older binary simply
doesn't know about columns a newer one added, and GORM ignores extra
columns it isn't asked to read. The one caution: never roll back past a
release that changed a `*_KEY_SALT`'s *meaning* (there hasn't been one,
and there shouldn't be) - that's a data-loss-shaped change, not a normal
rollback.

## 11. Pre-launch checklist

- [ ] `BASE_CHAIN_ID=8453` (mainnet) set explicitly, not left at the
      Sepolia default, once you intend to move real funds
- [ ] `JWT_SECRET` and every `*_KEY_SALT` set to real, randomly
      generated, securely stored values (never the
      `dev-only-change-me` placeholders)
- [ ] `SIGNATURE_AUTH_TOLERANCE_SECONDS` set deliberately, not left at
      its default without consideration (PLAN.md §12.3)
- [ ] `DB_TYPE=postgres` with a managed/backed-up database, not SQLite
- [ ] `STORAGE_DIR` mounted to durable storage
- [ ] Every funded-address salt (`FAUCET_KEY_SALT`,
      `CRYPTO_TREASURY_KEY_SALT`, etc.) has its derived address actually
      funded before the corresponding feature is enabled
- [ ] `DISCORD_WEBHOOK_URL` (or your own alerting integration) configured
      so faucet/treasury low-balance and error alerts actually reach
      someone
- [ ] A real Base RPC provider configured, not the public Sepolia/mainnet
      endpoint, if you expect meaningful traffic
- [ ] `internal/middleware/cors.go`'s `Access-Control-Allow-Origin`
      tightened from `*` to your actual frontend origin(s)
