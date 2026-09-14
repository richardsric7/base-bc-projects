# Deploying wallet-backend

This covers taking `wallet-backend` from a checkout to a running
production (or staging) instance. See `PLAN.md` for the design and
`.env.example` for every configuration value with its own explanation.
For the API surface itself (every route, its auth scheme, request/response
shapes), see §12 "API documentation (Swagger/OpenAPI)" below rather than
reading route registrations out of the Go source.

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
  volume. Use a dedicated provider for anything real - see §2 below for
  concrete options and how to get an API key from each.
- **wallet-payment-history-engine**, if you want indexed payment history
  beyond wallet-backend's own narrow submit-time records - see that
  project's own `DEPLOYMENT.md`. It's optional; wallet-backend runs fully
  without it.

## 2. Running against testnet vs. mainnet

**wallet-backend is one deployment per network, not one deployment
serving both.** There is no runtime "switch" on the server side (that
concept lives client-side, in `wallet-web`/`wallet-mobile`'s in-app
network toggle - see their own `PLAN.md`/`DEPLOYMENT.md`) - testnet and
mainnet each need their own running instance, with their own database,
their own funded operator addresses, and their own `.env`. Treat "point
this at mainnet" as standing up a second, independent deployment
alongside (not instead of) your testnet one, not as flipping a flag on
an existing one.

The only two variables that actually select the network are
`BASE_RPC_URL` and `BASE_CHAIN_ID` - every other configuration value
(secrets, feature toggles, integration credentials) is set the same way
regardless of network, just potentially to *different real values* per
environment (e.g. Flutterwave test-mode vs. live keys).

### Getting a Base RPC endpoint

The public endpoints (`https://sepolia.base.org` /
`https://mainnet.base.org`) are rate-limited and have no uptime SLA - fine
for local dev and light testing, not for production traffic. Get a
dedicated endpoint from one of:

| Provider | Where to get a key | Notes |
|---|---|---|
| **Alchemy** (alchemy.com) | Sign up → Create App → select "Base" and the network (Mainnet or Sepolia) → copy the HTTPS URL from the app's dashboard | Generous free tier; the most commonly used option for Base specifically |
| **Infura** (infura.io) | Sign up → Create a new API key → enable the "Base" network add-on → copy the endpoint URL | Same account works across many chains if you already use it elsewhere |
| **QuickNode** (quicknode.com) | Sign up → Create an endpoint → choose "Base" and the network → copy the HTTP Provider URL | Paid tiers scale further than the two above for high-volume production traffic |
| Self-hosted `base-node`/`op-geth` | Run your own Base full node (see [Base's own node docs](https://docs.base.org/) for the exact client) | Only worth it at real scale - eliminates a third-party dependency but is real infrastructure to operate |

Whichever you pick, the resulting URL is `BASE_RPC_URL` - it already
encodes both the provider and the network, so double-check you copied
the *Sepolia* URL for your testnet deployment and the *Mainnet* URL for
your mainnet one; providers show these as separate apps/endpoints.

### Testnet deployment - realistic `.env` values

```bash
BASE_RPC_URL=https://base-sepolia.g.alchemy.com/v2/<your-alchemy-key>
BASE_CHAIN_ID=84532
```

Fund every derived operator address (§4's table) with **Base Sepolia
ETH**, not real ETH - get it from a faucet such as
[Coinbase's Base Sepolia faucet](https://www.coinbase.com/faucets/base-ethereum-sepolia-faucet)
or [Alchemy's faucet](https://www.alchemy.com/faucets/base-sepolia) (both
free, both require a small mainnet ETH balance on a linked account or a
GitHub/social login, depending on the faucet's current anti-abuse
policy). Testnet curated tokens (for `ACTIVATION_REWARD_TOKEN_SYMBOL`
and asset trading) need their own test-token contracts deployed and
seeded via the admin surface - there's no faucet for arbitrary ERC-20s,
so either deploy your own test tokens or use whichever ones your team's
Base Sepolia integration already standardized on.

### Mainnet deployment - realistic `.env` values

```bash
BASE_RPC_URL=https://base-mainnet.g.alchemy.com/v2/<your-alchemy-key>
BASE_CHAIN_ID=8453
```

**Before this goes live**, confirm every item on the §13 pre-launch
checklist - the two most consequential mistakes are (a) leaving
`BASE_CHAIN_ID` at its testnet default, which points a "production"
deployment at play money instead of real funds and would silently
succeed at nothing real, and (b) reusing a `*_KEY_SALT` value from your
testnet deployment - a leaked testnet secret should never be able to
compromise a mainnet address. Generate every secret fresh per
environment (§4 shows how); never copy a testnet `.env` to a mainnet
host and only edit the RPC URL and chain ID.

Fund every derived operator address (§4's table) with **real Base ETH**
this time - transfer in from an exchange that supports Base withdrawals,
or bridge from Ethereum mainnet via the
[official Base Bridge](https://bridge.base.org/). Curated production
tokens (USDC, etc.) are added via the admin surface using their real
Base mainnet contract addresses - double check each one against
[Base's own contract registry](https://docs.base.org/base-contracts) or
the token issuer's own published address before seeding it; crediting
against the wrong contract address is not a recoverable mistake.

### Running both side by side

Nothing about the binary or Docker image differs between the two - it's
purely `.env`/environment-variable driven, so the same `docker build`
output can run as either, or you can run both simultaneously (e.g. one
`docker compose` stack per environment, or two Kubernetes deployments)
as long as each has its own database, its own `.env`, and its own set of
funded operator addresses. A common layout:

```bash
# Testnet, port 8080
docker run -p 8080:8080 --env-file .env.testnet wallet-backend

# Mainnet, port 8081 (or a separate host entirely)
docker run -p 8081:8080 --env-file .env.mainnet wallet-backend
```

## 3. Local development

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

## 4. Environment variables - what's required, realistic values, and where to get them

Every variable also has an inline comment in `.env.example`; this section
adds a realistic sample value and, where the value comes from a
third-party service rather than your own head, exactly where to go get
it. Treat every `*_KEY_SALT`, `JWT_SECRET`, and `RECOVERY_AUTHORITY_SALT`
as a real secret: generate with `openssl rand -hex 32` (a CSPRNG-backed
32-byte hex string), store in your platform's secret manager (AWS
Secrets Manager, GCP Secret Manager, HashiCorp Vault, or your CI/CD
provider's encrypted secrets - never a plain `.env` file committed
anywhere), and **never rotate one that has already derived a live
address** without a deliberate, funded migration plan - see the "why" column.

### Core service

| Variable | Realistic value | Notes |
|---|---|---|
| `PORT` | `8080` | Whatever your reverse proxy/load balancer expects. |
| `ORGANISATION` | `wallet-backend` | Free-text label, shows up in logs only. |
| `DB_TYPE` | `postgres` | `sqlite` for local dev only - see §1. |
| `DB_CONNECTION_STRING` | `host=db.internal.example user=wallet_backend password=<from secret manager> dbname=wallet_backend port=5432 sslmode=require` | From your database provider (RDS/Cloud SQL/self-hosted Postgres) - the password is whatever you set when provisioning that database's user, stored in your secret manager, not here in plaintext. `sslmode=require` (or stricter) in production. |
| `DB_AUTOMIGRATE` | `true` | See §5 for when to turn this off. |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` / `DB_CONN_MAX_LIFETIME_MINUTES` | `25` / `5` / `30` | Size up only if you run multiple replicas and are watching connection exhaustion on the Postgres side. |
| `BASE_RPC_URL` / `BASE_CHAIN_ID` | See §2 | The one setting that actually differs between testnet and mainnet. |

### Cache (optional)

| Variable | Realistic value | Notes |
|---|---|---|
| `ENABLE_CACHING` | `false` | Set `true` only if you're actually running Redis - nothing depends on it being on. |
| `REDIS_HOST` / `REDIS_PORT` | Your Redis provider's host/port (e.g. AWS ElastiCache endpoint, Redis Cloud endpoint, or `redis` if using the provided `docker-compose.yml`) | |
| `REDIS_PASSWORD` | From your Redis provider's dashboard/connection string | Leave blank for a local unauthenticated Redis. |

### Email

| Variable | Realistic value | Notes |
|---|---|---|
| `USE_CONSOLE_MAILER` | `false` in any real environment | `true` just logs emails to stdout - fine for local dev, never for anything a real user should receive (OTPs, recovery codes). |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` | e.g. `smtp.sendgrid.net` / `587` / `apikey` / `<your SendGrid API key>` | From your transactional email provider - SendGrid, Postmark, Amazon SES, or Mailtrap (`smtp.mailtrap.io`) for pre-production testing. Create an API key/SMTP credential in that provider's dashboard. |
| `MAIL_FROM` | `no-reply@your-domain.example` | Must be a sender address/domain verified with your email provider, or messages will be rejected or land in spam. |

### File storage

| Variable | Realistic value | Notes |
|---|---|---|
| `STORAGE_DIR` | `/data/uploads` (mounted to a persistent volume - see §8) | |
| `STORAGE_URL` | `/files` or a CDN/object-storage public prefix if you've swapped in one | |

### Alerting

| Variable | Realistic value | Notes |
|---|---|---|
| `DISCORD_WEBHOOK_URL` | `https://discord.com/api/webhooks/<id>/<token>` | In Discord: the target channel's Settings → Integrations → Webhooks → "New Webhook" → "Copy Webhook URL". Leave blank to disable operational alerts entirely (faucet/treasury low-balance, errors). |

### Auth and signing secrets

| Variable | Realistic value | Notes |
|---|---|---|
| `JWT_SECRET` | `openssl rand -hex 32` output | Signs admin-surface and servicelinks partner-login tokens. |
| `JWT_EXPIRY_MINUTES` | `60` | |
| `SIGNATURE_AUTH_TOLERANCE_SECONDS` | `300` | Bounds per-request signature replay window (PLAN.md §12.3) - widen only with a specific reason (e.g. clients with known clock drift), never "just in case". |
| `RECOVERY_AUTHORITY_SALT` | `openssl rand -hex 32` output | **Never rotate once any recovery attestation has been issued** - existing attestations would stop verifying. |
| `RECOVERY_OTP_TTL_MINUTES` | `15` | |
| `SAFE_DEPLOYER_KEY_SALT` | `openssl rand -hex 32` output | Pays gas to deploy every Safe wallet. Fund the derived address (logged at boot) with ETH. Safe to rotate - no ongoing authority over any wallet. |
| `RELAYER_KEY_SALT` / `RELAYER_POOL_SIZE` | `openssl rand -hex 32` output / `3`-`10` depending on expected concurrent shared-access approvals | Fund every one of `RELAYER_POOL_SIZE` derived addresses (logged at boot) with ETH. Same harmless-rotation property as the deployer key. |

### KYC (optional - leave blank to disable)

| Variable | Realistic value | Notes |
|---|---|---|
| `SUMSUB_BASE_URL` | `https://api.sumsub.com` | Sumsub's fixed API host - no need to change. |
| `SUMSUB_TOKEN` / `SUMSUB_SECRET_KEY` | From Sumsub | [cockpit.sumsub.com](https://cockpit.sumsub.com) → Settings → API keys → create an App Token + Secret Key pair. Use a sandbox app for testnet, a live app for mainnet. |
| `DOJA_SECRET_KEY` | From Dojah | [dojah.io](https://dojah.io) dashboard → API keys. |

### Fiat on/off-ramp - Flutterwave (optional)

| Variable | Realistic value | Notes |
|---|---|---|
| `FLUTTERWAVE_SECRET_KEY` | `FLWSECK_TEST-...` (test mode) or `FLWSECK-...` (live) | [dashboard.flutterwave.com](https://dashboard.flutterwave.com) → Settings → API. Use the test key for testnet, live key for mainnet - these are independent of `BASE_CHAIN_ID` and must be switched deliberately. |
| `FLUTTERWAVE_SECRET_HASH` | A value you set yourself in Flutterwave's dashboard | Dashboard → Settings → Webhooks → set a "Secret Hash", then put that same value here - it's how the webhook handler verifies a callback actually came from Flutterwave. |

### Faucet / activation

| Variable | Realistic value | Notes |
|---|---|---|
| `FAUCET_KEY_SALT` | `openssl rand -hex 32` output | Fund the derived address with ETH (and the reward token, if set) before enabling. |
| `ACTIVATION_REWARD_TOKEN_SYMBOL` | e.g. `USDC`, or blank | Must match a `CuratedToken.Symbol` already seeded via the admin surface. |
| `FAUCET_LOW_BALANCE_THRESHOLD_ETH` | e.g. `0.5` | `0` disables the check. Set high enough that the Discord alert gives you time to top up before the faucet actually runs dry. |

### Stablerail (optional)

| Variable | Realistic value | Notes |
|---|---|---|
| `STABLERAIL_ENABLED` | `false` unless you have a live partner integration | Every route fails closed with "not enabled" while this is false, regardless of the API key. |
| `STABLERAIL_API_KEY` | From your Stablerail/Strails partner onboarding | Provided during partner onboarding - contact your Stablerail account representative; there is no self-serve signup at the time of writing. |
| `STABLERAIL_BASE_URL` | `https://beta.stablesrail.io/v1` (sandbox) or the live base URL your partner contact provides | Confirm the live-mode base URL with your account rep before going to production - don't assume it's the same host with a different path. |

### Crypto deposits/withdrawals - OneLiquidity (optional)

| Variable | Realistic value | Notes |
|---|---|---|
| `ONELIQUIDITY_BASE_URL` | `https://sandbox-api.oneliquidity.technology` (sandbox) or your assigned production URL | The deposit poller only runs once `ONELIQUIDITY_TOKEN` is set. |
| `ONELIQUIDITY_TOKEN` | From OneLiquidity | Issued during OneLiquidity partner onboarding. |
| `CRYPTO_WALLET_DOMAIN` | `wallet-backend` or your own product name | Arbitrary - just the suffix OneLiquidity subwallet UIDs are keyed by (`username@this-value`). |
| `CRYPTO_TREASURY_KEY_SALT` | `openssl rand -hex 32` output | Fund the derived address with every curated token before enabling deposits. |
| `CRYPTO_WITHDRAWAL_SERVICE_FEE_PERCENT` | e.g. `1.0` | Your own pricing decision. |

### Market making

| Variable | Realistic value | Notes |
|---|---|---|
| `MARKET_ESCROW_KEY_SALT` | `openssl rand -hex 32` output | Rotating orphans standing maker approvals - treat as effectively permanent once live. |

### Tokenization

| Variable | Realistic value | Notes |
|---|---|---|
| `TOKENIZATION_ISSUER_KEY_SALT` / `TOKENIZATION_DISTRIBUTION_KEY_SALT` | `openssl rand -hex 32` output, two distinct values | Rotating either orphans on-chain contract ownership of every already-minted asset - effectively permanent once any asset is live. |
| `TOKENIZATION_TOKEN_LIMIT` | `0` (no cap) or a real cap, e.g. `1000000` | Your own product decision. |

### Patron / subscriptions

| Variable | Realistic value | Notes |
|---|---|---|
| `PATRON_FEE_WALLET_SALT` | `openssl rand -hex 32` output | |
| `PATRON_VAT_PERCENT` | e.g. `7.5` (Nigeria's VAT rate) or `0` | Set to whatever's legally correct for your jurisdiction and product structure - this is a tax/legal decision, not a technical one. |

### Servicelinks / shared-access

| Variable | Realistic value | Notes |
|---|---|---|
| `SERVICELINK_APPROVAL_TTL_MINUTES` | `10` | |
| `PENDING_ACTION_TTL_MINUTES` | `1440` (24h) | |

### Geo-IP and shortlinks

| Variable | Realistic value | Notes |
|---|---|---|
| `GEOIP_BASE_URL` | `https://ipapi.co` | Free tier available at [ipapi.co](https://ipapi.co) (rate-limited); paid tiers for production volume. Leave blank to disable geo-IP entirely - registration works identically either way. |
| `SHORTLINK_BASE_URL` | `https://link.your-domain.example` | The public origin your generated QR codes/short URLs resolve through - point your own DNS at wherever this service is reachable. |

### Wallet recovery Branch B (optional, opt-in)

| Variable | Realistic value | Notes |
|---|---|---|
| `RECOVERY_OPERATOR_KEY_SALTS` | Three (or more) **distinct** `openssl rand -hex 32` outputs, comma-separated | One per platform trusted operator - see PLAN.md §15. Held by different people/systems in practice (e.g. different secret-manager entries with different access grants), not just different config lines, or the "N-of-M" protection is theater. Never reuse a value from any other row in this table. |
| `RECOVERY_SERVICE_THRESHOLD` | `0` (majority) or an explicit number, e.g. `2` | How many operators must co-sign a real recovery. |
| `WALLET_RECOVERY_FEE_ETH` | `0` (free) or e.g. `0.001` | Your own product/anti-abuse decision. |

## 5. Database migrations

`DB_AUTOMIGRATE=true` (the default) runs GORM's `AutoMigrate` for every
component's models at boot - additive schema changes (new tables/columns)
apply automatically on deploy. This is safe for this project's migration
style (no destructive auto-migrations are ever generated), but for a
larger production deployment where you want migrations reviewed and
applied as a distinct step rather than happening implicitly on every
pod restart, set `DB_AUTOMIGRATE=false` and run the binary once with it
`true` (or write a small migration-only entrypoint) as part of your
release process instead of at every boot.

## 6. Building and running

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

## 7. CORS

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

## 8. Health check and readiness

`GET /` returns `{"service", "status": "ok", "chainId"}` with no
authentication required - use it for your load balancer's or
orchestrator's health/readiness probe. It does not check database or
Base RPC connectivity, only that the process is up and knows its own
configuration; a deeper check (DB ping, RPC `eth_chainId`) is worth
adding to your monitoring separately if you need it, since a false
"ok" here while the database is down will otherwise only surface as
5xx errors on the next real request.

## 9. File storage

`STORAGE_DIR` (default `./data/uploads`) is where uploaded files
(tokenization documents, etc.) land on local disk. **This does not
survive a container restart or scale beyond one replica** unless backed
by a persistent volume (or you swap in an object-storage-backed
implementation) - mount `STORAGE_DIR` to durable storage before any
production traffic depends on uploaded documents persisting.

## 10. Order of deployment relative to the other ported projects

wallet-backend has no hard dependency on the other two projects and can
be deployed alone. If you're standing up the full stack:

1. **wallet-backend first** - it owns the `users`/`user_wallets`/
   `curated_tokens` tables the other two projects read.
2. **wallet-payment-history-engine second**, pointed at the *same*
   Postgres database (not a separate one - see its own `DEPLOYMENT.md`).
   It only reads wallet-backend's tables, never migrates or writes them.
3. **wallet-web last**, pointed at wallet-backend's public URL - see
   `wallet-web/DEPLOYMENT.md` §2-4 for how its own build-time
   `VITE_WALLET_BACKEND_URL_TESTNET`/`_MAINNET` config and in-app
   network switch correspond to *this* service's testnet/mainnet
   deployments (§2 above) - and, once available,
   wallet-payment-history-engine's indexed history via wallet-backend's
   own `/v1/payments/history/:address` route.

Each of wallet-backend's testnet and mainnet deployments (§2) is a
distinct target for this same ordering - a `wallet-web` testnet build
points at this service's testnet deployment, a mainnet build (or the
mainnet side of a build offering both) at the mainnet one.

## 11. Rollback

Since migrations here are additive-only `AutoMigrate` calls, rolling back
to a previous image version is generally safe - an older binary simply
doesn't know about columns a newer one added, and GORM ignores extra
columns it isn't asked to read. The one caution: never roll back past a
release that changed a `*_KEY_SALT`'s *meaning* (there hasn't been one,
and there shouldn't be) - that's a data-loss-shaped change, not a normal
rollback.

## 12. API documentation (Swagger/OpenAPI)

Once running, every route (all ~150 of them, across every component) is
documented at `GET /swagger/` - an interactive Swagger UI backed by a
hand-authored `GET /swagger/openapi.json` spec (`internal/docs`). It
documents each route's auth scheme (`SignatureAuth`'s four headers,
`ApiKeyAuth`'s `X-API-Key`, or `BearerAuth`'s JWT), request/response
shapes, and realistic error responses - use it as the primary reference
for integrating a client against this API, rather than reading
`internal/components/*/controllers` directly. The Swagger UI's own
assets load from a CDN (`cdn.jsdelivr.net`), so it needs outbound
internet access from whichever browser is viewing it (not from the
server itself) to render - the underlying `openapi.json` is fully
self-hosted and usable with any offline OpenAPI tool (e.g. `redocly`,
Postman's "Import" from URL) if that's a constraint for you.

## 13. Pre-launch checklist

- [ ] `BASE_CHAIN_ID=8453` (mainnet) set explicitly, not left at the
      Sepolia default, once you intend to move real funds - see §2
- [ ] `JWT_SECRET` and every `*_KEY_SALT` set to real, randomly
      generated, securely stored values (never the
      `dev-only-change-me` placeholders), and **distinct from whatever
      your testnet deployment uses** - see §4
- [ ] `SIGNATURE_AUTH_TOLERANCE_SECONDS` set deliberately, not left at
      its default without consideration (PLAN.md §12.3)
- [ ] `DB_TYPE=postgres` with a managed/backed-up database, not SQLite
- [ ] `STORAGE_DIR` mounted to durable storage
- [ ] Every funded-address salt (`FAUCET_KEY_SALT`,
      `CRYPTO_TREASURY_KEY_SALT`, `SAFE_DEPLOYER_KEY_SALT`,
      `RELAYER_KEY_SALT`, etc.) has its derived address actually funded
      with **real** ETH/tokens (not testnet ones) before the
      corresponding feature is enabled
- [ ] `DISCORD_WEBHOOK_URL` (or your own alerting integration) configured
      so faucet/treasury low-balance and error alerts actually reach
      someone
- [ ] A real, dedicated Base mainnet RPC provider configured (§2), not
      the public endpoint, if you expect meaningful traffic
- [ ] `internal/middleware/cors.go`'s `Access-Control-Allow-Origin`
      tightened from `*` to your actual frontend origin(s)
- [ ] Every third-party integration's credentials (Flutterwave, Sumsub/
      Dojah, Stablerail, OneLiquidity) switched from sandbox/test-mode to
      live values, deliberately, not just carried over from testnet - §4
      flags which ones have this test/live distinction
- [ ] Curated token contract addresses seeded via the admin surface
      verified against an authoritative source (Base's own contract
      registry, or the token issuer directly) - not copied from a
      testnet deployment's addresses, which point at different contracts
- [ ] `GET /swagger/` reviewed once against this deployment to confirm
      it's reachable and matches expectations for whoever will integrate
      against it (§12)
