# wallet-payment-history-engine

A headless indexer that watches Base for payments touching wallet-backend's
users, and writes a canonical, chain-observed `payment_history` table -
giving the wallet app a standard, searchable payment history without
querying the blockchain on every read. This is the Base port of
`trovo-wallet-payment-history-engine` (the original Stellar/Horizon
engine); see [`PLAN.md`](./PLAN.md) for the full audit, substitution
table, and design rationale.

No HTTP server - it only writes to the database it shares with
wallet-backend.

## Architecture

```
main.go                      # boot: open shared DB, start watchers, block forever
internal/
├── sharedconfig/            # GlobalConfig + Env loader
├── db/                      # OpenDB (one shared Postgres instance) + this engine's own migrations
├── network/                 # ethclient wrapper: log filters, block scanning, Transfer decode
├── notify/                  # Provider interface + Noop default (real-time payment notification hook)
├── alerting/                # Notifier interface + Noop/Discord (operational alerts)
└── engine/
    ├── models/               # PaymentHistory, TrackedWallet, IndexerCursor (owned + migrated here)
    │                         # + read-only mirrors of wallet-backend's user_wallets/curated_tokens
    ├── tracking/              # wallet-discovery sweep: mirrors user_wallets -> TrackedWallet
    ├── indexer/               # log-polling + transaction classification + the one canonical writer
    └── backfill/              # per-wallet from-genesis history reconstruction for newly tracked wallets
```

Two independent watchers land on the same `indexer.SavePaymentHistory` -
the only code path allowed to write a `PaymentHistory` row:

- **Live watcher** (`internal/engine/indexer`): polls every `POLL_INTERVAL_SECONDS`
  for new blocks since the last persisted cursor, classifying each match
  as a plain payment, mint/burn, or swap (see PLAN.md §3.3).
- **Backfill watcher** (`internal/engine/backfill`): when a wallet is newly
  tracked, does a one-shot scan of its full history from `BACKFILL_FLOOR_BLOCK`
  to the current tip before the live watcher takes over the going-forward tail.

The writer is idempotent (`ON CONFLICT (transaction_hash, log_index) DO NOTHING`),
so a restart safely re-processes at most one partially-done block range.

## Running

```bash
cp .env.example .env   # adjust as needed - every value has a working default
go run .
```

Point `DB_CONNECTION_STRING` at the **same** database wallet-backend uses.
This engine only ever migrates its own three tables
(`payment_history`, `tracked_wallets`, `indexer_cursors`) - it reads
wallet-backend's `user_wallets`/`curated_tokens` tables but never migrates
or writes them.

## Testing

```bash
go build ./...
go vet ./...
go test ./...
```

Live-network verification against Base Sepolia requires outbound RPC
access to `sepolia.base.org`, which this development sandbox's egress
policy blocks; `go test ./...` and a local boot against an unreachable
RPC endpoint both confirm the engine migrates its schema and runs its
poll loops without crashing (backoff-and-retry on every RPC error,
`defer recover()` around each poll iteration) - verify the live RPC path
itself in an environment with real Base Sepolia access before deploying.

## Coordination note

wallet-backend's own `payments.PaymentHistory` model only captures
payments it itself submitted - it has no way to see incoming payments or
anything submitted through another channel. Pointing wallet-backend's
read-side payment history API at this engine's richer table is a
follow-up change to wallet-backend, tracked but not implemented here -
see PLAN.md §6/§7 (Phase 9).
