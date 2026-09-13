# Deploying wallet-payment-history-engine

A headless indexer with no HTTP surface at all - there is no port to
expose, no health-check route, nothing for a load balancer to point at.
It runs, polls Base, and writes to a database. See `PLAN.md` for the
design and `.env.example` for every configuration value.

## 1. Prerequisites

- **Go 1.26** to build, or Docker.
- **The same PostgreSQL database `wallet-backend` uses** - not a separate
  one. This is the load-bearing deployment fact for this project: it
  reads wallet-backend's `user_wallets`/`curated_tokens` tables directly
  and only ever migrates its own three tables (`payment_history`,
  `tracked_wallets`, `indexer_cursors`). **Deploy `wallet-backend` first**
  so those tables already exist - see its own `DEPLOYMENT.md`.
- **A Base RPC endpoint** - same guidance as wallet-backend's own
  `DEPLOYMENT.md`: the public Sepolia RPC is fine for testing, use a
  dedicated provider for production polling volume. `BASE_RPC_URL`/
  `BASE_CHAIN_ID` **must match wallet-backend's own values** - this
  engine and wallet-backend must always agree on which chain they're
  both looking at.

## 2. Local development

```bash
cp .env.example .env
go run .
```

SQLite (`DB_TYPE=sqlite`, the default) works for local dev, but note it
won't have wallet-backend's `user_wallets`/`curated_tokens` tables unless
you've also run wallet-backend against that same SQLite file first - in
practice, local dev of this engine is easiest against the same
`docker compose`-provisioned Postgres wallet-backend's own
`DEPLOYMENT.md` sets up, not a separate SQLite file.

## 3. Environment variables

| Variable | Notes |
|---|---|
| `DB_TYPE` / `DB_CONNECTION_STRING` | **Must point at wallet-backend's own database.** This is not optional or a style choice - the engine's read-only queries against `user_wallets`/`curated_tokens` assume those tables exist with wallet-backend's exact schema. |
| `DB_AUTOMIGRATE` | Migrates only this engine's own three tables (see `internal/db.MigrateDB`'s doc comment) - never touches wallet-backend's tables regardless of this setting. |
| `BASE_RPC_URL` / `BASE_CHAIN_ID` | Must match wallet-backend's configuration for the same deployment. |
| `POLL_INTERVAL_SECONDS` | How often the live watcher checks for new blocks (default 4s, matching wallet-backend's own `AddressWatcher` cadence). Lower values mean fresher history at the cost of more RPC calls. |
| `BACKFILL_FLOOR_BLOCK` | The earliest block the backfill watcher will scan for a newly tracked wallet. **Set this to your app's actual launch block on mainnet** before going live - leaving it at `0` (genesis) means every newly tracked wallet triggers a full-chain-history scan, which is wasted work for a wallet that couldn't possibly have transacted before your app existed. |
| `DISCORD_WEBHOOK_URL` | Optional operational alerting (indexer errors, backfill failures). Unset means alerts are silently dropped - the engine's core behavior never depends on this being configured, but you should configure it in production so a stuck indexer doesn't fail silently. |

No secrets are unique to this project - it derives no keys and holds no
funds, only reads chain data and wallet-backend's own tables.

## 4. Building and running

```bash
docker build -t wallet-payment-history-engine .
docker run --env-file .env wallet-payment-history-engine
```

No `-p` port mapping - there is nothing listening. From source:
`go build -o wallet-payment-history-engine . && ./wallet-payment-history-engine`.

## 5. Running alongside wallet-backend

This engine is a separate deployable, not a component you add to
wallet-backend's own container - run it as its own process/pod pointed
at the same database. A minimal `docker-compose.yml` addition to
wallet-backend's own compose file, run in the same network:

```yaml
  payment-history-engine:
    build: ../wallet-payment-history-engine
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      DB_TYPE: postgres
      DB_CONNECTION_STRING: "host=postgres user=wallet password=wallet dbname=wallet_backend port=5432 sslmode=disable"
      DB_AUTOMIGRATE: "true"
      BASE_RPC_URL: https://sepolia.base.org
      BASE_CHAIN_ID: 84532
```

(No `ports:` entry - it needs none.)

## 6. Health/liveness monitoring

There's no `/` health endpoint to probe (see the top of this document -
headless by design). For container orchestration liveness checks, use
process liveness (is the container running at all) rather than an HTTP
probe, and rely on `DISCORD_WEBHOOK_URL` alerting plus watching the
`indexer_cursors` table's `last_block` value for staleness (it should
track close to the chain's current tip; a `last_block` that stops
advancing while the container is still "up" means the poll loop is stuck
despite the process being alive - alert on that gap, not just on process
liveness).

## 7. Scaling

**Run exactly one instance.** The live watcher's cursor
(`indexer_cursors`, a single row) has no locking against concurrent
writers - two instances polling the same range would double-process
(harmlessly, since `SavePaymentHistory` is idempotent) but would also
race on cursor writes in a way that could leave the cursor behind the
true tip. If you need higher throughput, reduce `POLL_INTERVAL_SECONDS`
first; horizontal scaling of this specific engine was not part of this
port's design.

## 8. Pre-launch checklist

- [ ] Points at wallet-backend's actual production database, confirmed
      by checking that `user_wallets`/`curated_tokens` queries return
      real data, not empty tables
- [ ] `BASE_RPC_URL`/`BASE_CHAIN_ID` match wallet-backend's own
      production values exactly
- [ ] `BACKFILL_FLOOR_BLOCK` set to your actual launch block, not left at
      genesis, before mainnet cutover
- [ ] `DISCORD_WEBHOOK_URL` (or equivalent) configured, and a monitor on
      `indexer_cursors.last_block` staleness in place
- [ ] Exactly one instance running against a given database
