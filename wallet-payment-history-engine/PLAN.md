# wallet-payment-history-engine — Full Base Blockchain Port of the Trovo Payment History Engine

**Status: design only — no implementation.** This document is the complete
port plan. Do not write code from it until explicitly instructed.

## 0. What this service is, and why it's separate from `wallet-backend`

In the original Trovo stack, "payment history" is **not** built by the main
wallet API. It's built by a second, standalone Go service —
`trovo-wallet-payment-history-engine` — that does nothing but watch the
Stellar network, recognize which operations touch a Trovo wallet, and write
rows into a `payment_history` table that the main wallet API's read-only
query endpoints (`GET /v1/users/payments/:publicKey`, the servicelinks
partner equivalent) serve back to clients. The wallet API itself never talks
to Horizon to answer a "show me my history" request — it just queries
Postgres. That's the entire value proposition, restated in the terms this
port's own author gave it: **a standard, searchable payment history without
ever touching the blockchain at query time.**

This port mirrors that separation exactly: `wallet-payment-history-engine`
is its own Go module, its own deployable, with its own `go.mod` — a sibling
project to `wallet-backend`, not a component inside it. It shares
`wallet-backend`'s Postgres database (reads `users`/`user_wallets` to know
what to track; writes the shared `payment_history` table `wallet-backend`'s
`payments` component already queries) but ships and runs independently, the
same relationship the original had between its two repos.

### How this plan was produced

Two independent, fully-read audits, not guesswork:

1. **`trovo-wallet-monorepo/backend`** (the main wallet API) — read in full:
   `internal/components/payments/services/history.go` (the paginated
   read-side query, `GetPaymentHistory`/`GetCryptoDepositHistory`/
   `GetCryptoWithdrawalHistory`), `internal/components/payments/models/
   payment_history.go`, `internal/components/users/blockchain/history.go`
   and `payment_history_transformer.go`, `internal/components/users/
   services/websockets.go`, `pay.go`, `server_payments.go`, every route
   registration touching history, `internal/db/main.go`, and `main.go`'s
   boot-time Horizon-streaming worker. **Finding: every write-path function
   in this repo is a dead stub** (`ProcessStreamPaymentOperation`/
   `ProcessRetrievedPaymentOperation` return zero-value structs and do
   nothing) — this repo only *reads* `payment_history`; it never populates
   it. `README.md:104-108` confirms this is deliberate, pointing at the
   separate engine repo.
2. **`trovo-wallet-payment-history-engine`** (the real engine) — its
   default branch (`main`) is an *empty* repository; the actual code lives
   on its `dev` branch, which this plan is grounded in, read in full:
   `main.go` (1228 lines — `MonitorPaymentStream`, `ProcessOperation`,
   `MonitorTradeStream`/`ProcessTrade`, cursor handling), `internal/
   components/payments/services/procedure.go` (`SavePaymentHistory`,
   `TrackUserWallet`), `internal/components/payments/models/
   payment_history.go` and `trades.go`, `internal/db/main.go`, `internal/
   components/users/{db,models}` (its read-only mirror of `User`/
   `UserWallet`), and `internal/network/main.go`.

Every "Original:" claim below is traceable to a specific file/function in
one of these two repos.

## 1. Original architecture, exhaustively

### 1.1 Data model (`trovo-wallet-payment-history-engine`, `dev` branch)

```go
// internal/components/payments/models/payment_history.go
type PaymentHistory struct {
    ID                    string    // Horizon operation ID (numeric string)
    TransactionType       string    // "PAYMENT" | "MINT TOKEN" | "BURN TOKEN" | "SWAP <A>><B>" | "TRADE" (dead)
    TransactionDate       time.Time
    From                  *string   // "<Name>[<alias>]" — nil if sender isn't a tracked Trovo wallet
    FromPublicKey         string    // raw Stellar G... address, always set
    To                    *string   // same shape as From
    ToPublicKey           string
    Memo                  *string   // Stellar text memo, nil if absent
    AssetIssuer           *string   // nil for the native asset
    AssetCode             string
    Amount                string    // decimal-as-string, never a float
    TransactionID         string    // Stellar transaction hash
    PT                    string    // Horizon paging token of the operation (json:"-")
    SourceAccountSequence string    // json:"-"
}

type TrackedWallet struct {   // this engine's own bookkeeping, NOT queried by the wallet API
    ID            string
    PublicKey     string  `gorm:"unique"`
    TempPublicKey *string `gorm:"unique"`
    Alias         string
    Name          string
}
type TrackedPublicKey struct { PublicKey string `gorm:"primaryKey"` } // work queue for MonitorPublicKeyPaymentStream
type MonitoredCursor struct { ID uint64; LastCursor string }          // meant to persist stream resume position
```

**Bug found: the two repos disagree on `PaymentHistory`'s own unique key.**
`trovo-wallet-monorepo/backend`'s copy of this model declares the
composite unique index over `{TransactionType, TransactionID, PT,
SourceAccountSequence}` (4 columns). The real engine's copy — the one that
actually runs `AutoMigrate` against production — declares the *same named*
index (`idx_payment_history_unique_key`) over `{TransactionType,
FromPublicKey, ToPublicKey, AssetCode, Amount, TransactionID, PT,
SourceAccountSequence}` (8 columns). Two services pointed at one physical
table, disagreeing about the table's own dedup semantics, is exactly the
kind of schema drift a from-scratch port must not reproduce — see §4.

### 1.2 Write path: how a row actually gets created

There is exactly one producer of `payment_history` rows:
`MonitorPaymentStream` (`main.go:395-471`) → Horizon
`client.StreamPayments` (a payments-filtered operation stream, not the raw
operations stream `trovo-wallet-monorepo/backend`'s dead `main.go` code
independently also subscribes to) → a 200,000-deep buffered channel → a
**single** worker goroutine (`main.go:441-446` — a second worker is
present in source but commented out) → `ProcessOperation` (`main.go:
612-813`).

`ProcessOperation` type-switches on exactly four Horizon operation types —
`payment`, `create_account`, `path_payment_strict_send`, `path_payment` —
and for each: looks up **both** the from- and to-address in the
`TrackedWallet` table (`WHERE (public_key = ? OR temp_public_key = ?) OR
(public_key = ? OR temp_public_key = ?)`), **drops the operation entirely
if neither side is a tracked Trovo wallet**, resolves whichever side(s)
matched to a `"Name[alias]"` label, derives a `TransactionType` (a plain
`"PAYMENT"`; `"MINT TOKEN"`/`"BURN TOKEN"` when the issuer is the sender/
receiver; `"SWAP <src>><dst>"` for path payments), and calls
`SavePaymentHistory` (`procedure.go:133-214`), which does a naive
create-then-catch-constraint-then-update upsert against the 8-column key
above.

**Bug found, confirmed in both repos independently: `path_payment_strict_
receive` — the third and most common Stellar path-payment variant — is
never handled.** Neither `ProcessOperation` here nor its dead counterpart
in `trovo-wallet-monorepo/backend`'s `main.go` branches on it. Any
strict-receive swap silently produces no history row at all.

**Bug found: cursor persistence is designed but was never turned on.**
`GetLastCursor` (`main.go:334-356`) reads `MonitoredCursor` from the DB (or
an env-var override) and `SaveLastCursor` (`main.go:370-393`) exists to
write it back — but `SaveLastCursor` is called from exactly nowhere except
a commented-out `defer` at the top of `ProcessOperation`
(`main.go:613`, `// defer SaveLastCursor(o.PagingToken(), roachDB)`). In
practice `MonitorPaymentStream` starts its global stream with **no**
cursor at all (`opsRequest = horizonclient.OperationRequest{Join:
"transactions"}`, `main.go:430-433` — the cursor-aware branch above it is
dead code, since `GetLastCursor` always returns `"0"` when
`MonitoredCursor` has never been written, and `"0"` fails the `!= "0"`
check that would have used it). **Net effect: every restart or stream
error resumes from live tip, silently losing whatever happened during the
downtime.** This is the single most important behavior gap to actually fix
rather than reproduce (§4, §5).

**Wallet-tracking bug found: `TrackUserWallet` (`procedure.go:19-131`)
deletes a wallet's `TrackedWallet` row unconditionally before checking
whether one exists** (`roachDB.Where(...).Delete(...)` immediately
followed by `roachDB.Where(...).First(&existingWallet)`), which means the
lookup **always** reports not-found and the "update existing" branch
(`procedure.go:100-129`) can never execute — every tracking pass is really
a delete-and-recreate-with-a-fresh-UUID, not an update. Functionally
harmless (public key stays unique-indexed) but it's dead code masquerading
as live code, and `TrackedPublicKey` seeding only happens from the
reachable "create" branch as a side effect of this.

**Reconnect/backpressure**: outer loop is `for { MonitorPaymentStream(...);
time.Sleep(5s) }` (`main.go` boot wiring) — a bare fixed-delay retry, no
backoff, no jitter, no panic recovery, no circuit breaker. The 200,000-slot
channel is generous but silent under sustained overload (blocks the
Horizon SSE reader rather than shedding or alerting).

### 1.3 Wallet discovery: `TrackUserWallet` / the two boot-time sweepers

Two `go func(){ for {...} }()` loops in `main.go` (`:198-260`,
`:262-309`) continuously (a) find `UserWallet` rows in the main app DB with
`tracked = 0`, batch-process them through `TrackUserWallet` to populate
`TrackedWallet`, and (b) drain a `TrackedPublicKey` work queue by spinning
up one `MonitorPublicKeyPaymentStream` goroutine per key — a **per-account,
from-genesis** Horizon stream used to backfill a *newly* tracked wallet's
full history (as opposed to `MonitorPaymentStream`'s single **global,
live-only** stream that catches everything going forward). This
backfill-vs-live split is a real, worth-keeping design idea: new wallets
need their history reconstructed once; already-known wallets just need the
live tail.

### 1.4 Bundled but unrelated: market-making trade-fee collection

`MonitorTradeStream`/`ProcessTrade` (`main.go:473-541`, `:815-1228`) is a
**second, unrelated subsystem** riding in the same binary: it streams
Stellar `Trade` events, matches them against this deployment's own
`MarketOffer` rows, and submits a fee-collection payment transaction to a
configured fee wallet. Its own "save this as `payment_history`" branches
are gated behind a hardcoded `enable := false` (`main.go:1129`) — dead on
arrival, never active in production. This is a market-maker fee-skimming
bot, not a payment-history concern; `wallet-backend`'s own `market`
component (Phase 7, already built) is the Base home for market-making
fee logic. **Not ported here** — see §6.

### 1.5 Read path (unchanged from the earlier `wallet-backend` audit, `trovo-wallet-monorepo/backend`)

`GetPaymentHistory` (`payments/services/history.go`) — offset-paginated,
filters: `transactionType` (exact for `"payment"`, prefix for `"swap"`),
`fromPublicKey`/`toPublicKey` (exact, 56-char-gated), `assetCode`,
`assetIssuer` (56-char-gated), `name` (LIKE on the resolved alias/name
column), `memo` (LIKE), `transactionID` (exact, oddly lower-cased),
`amount`/`dateBetween` (pipe-delimited ranges), `orderby`/`order`
(passed into `.Order()` with **no column allowlist** — a real
SQL-injection-shaped smell, not to be repeated), `limit`/`page`. Auth: the
caller must own the target wallet or hold shared-access signer rights on
it (self-service route), or — for the servicelinks partner route — the
target wallet must belong to a user the calling service link created.
20-second Redis cache keyed on the full request URI.

### 1.6 What never got real-time delivery

`websockets.go`'s payment/swap stream messages are permanently empty
(§1.2's stub bug), and a second, wrong-type-assertion crash bug
(`interface{}(o).(operations.AccountMerge)` used to unpack a `ChangeTrust`
operation) panics the connection's goroutine the first time a
`change_trust` operation streams through — outside Gin's request-level
recovery, so it takes the whole process down. Real-time delivery is a
place this port can straightforwardly do better than the original ever
actually worked (§3.5), but it inherits none of the original's code.

## 2. Base substitution table

| Stellar/Horizon primitive | Base/EVM equivalent | Notes |
|---|---|---|
| Horizon operation SSE stream (`client.StreamPayments`/`StreamOperations`) | `eth_getLogs` polling for ERC-20 `Transfer` events + per-block scan of native-value transactions | Base (any OP-Stack chain) has no native push/streaming primitive as robust as Horizon's SSE; polling is the standard, and `wallet-backend`'s own `internal/network.AddressWatcher` (built in its Phase 8) already does exactly this poll-for-Transfer-logs pattern — this engine generalizes and persists it (§3.2) rather than reinventing it. |
| `payment` operation (native or credit asset) | ERC-20 `Transfer(from, to, value)` log **for token transfers**; a transaction's own `value` field **for native ETH transfers** (no log emitted) | Two independent signal sources must both be watched — see §3.2. |
| `create_account` operation | **No equivalent — dropped.** | Every EVM address exists the instant it's derived; there is no on-chain "account creation" event to observe. A wallet's very first inbound transfer is just an ordinary `Transfer`/native-value event like any other. |
| `path_payment_strict_send`/`_receive`/`path_payment` (built-in DEX swap) | A DEX router `Swap` event (Uniswap-V3-style pools; `wallet-backend`'s `swaps` component already builds router calls generically) | Classify a transaction as a swap by decoding the pool's `Swap` topic when present, falling back to "two Transfer events in one tx, one out one in, for the tracked wallet" as a router-agnostic heuristic — see §3.3. |
| Horizon paging token (`PT`) as the resume cursor | **Block number** (+ log index for uniqueness within a block) | Ethereum JSON-RPC's native, universally-supported resumption unit; strictly simpler and more robust than Horizon's opaque paging token. |
| Stellar text memo | **Dropped — no protocol equivalent.** | Base/EVM transactions carry no memo field at the protocol level (only arbitrary `data`, which is calldata, not a payment annotation). `PaymentHistory.Memo` is simply omitted from the Base schema; a future product need for "payment notes" would be an off-chain, application-level feature, not something this indexer can observe on-chain. |
| Temp-account trustline bootstrapping / `TempPublicKey` tracking | **Dropped — no protocol equivalent.** | Base has no trustline concept at all (any address can hold any ERC-20 balance unconditionally, per `wallet-backend/PLAN.md` §2's very first substitution row) — there is nothing analogous to watch a temp signer key for. |
| A second CockroachDB purely for `TrackedWallet`/cursor bookkeeping | **One shared Postgres database** (the same one `wallet-backend` uses) | `wallet-backend/PLAN.md`'s own "Open decisions" §11 already flagged this exact split as needless for the Stellar port; this plan carries that decision forward rather than re-opening it. |
| `MINT TOKEN`/`BURN TOKEN` transaction-type inference (`pmt.From == pmt.Issuer`) | ERC-20 `Transfer` **from/to the zero address** | The standard EVM mint/burn convention — `Transfer(0x0, to, amount)` is a mint, `Transfer(from, 0x0, amount)` is a burn — requires no issuer-address bookkeeping at all, unlike Stellar's per-asset issuer-account check. |

## 3. Base design

### 3.1 Module layout

A standalone Go module, `wallet-payment-history-engine`, matching
`wallet-backend`'s conventions (Go, GORM, `github.com/shopspring/decimal`
for every amount, `github.com/ethereum/go-ethereum` for all chain access)
but with **no HTTP server at all** — this is a headless indexer, exactly
matching the original's true architecture (§1: `trovo-wallet-payment-
history-engine`'s `main.go` never constructs a `gin.Engine`, despite
vestigial, unused Gin middleware files sitting in that repo).

```
wallet-payment-history-engine/
├── main.go                      # boot: open shared DB, start watchers, block forever
├── internal/
│   ├── sharedconfig/            # GlobalConfig + Env loader — same pattern as wallet-backend
│   ├── db/                      # OpenDB (one shared Postgres instance, no second database)
│   ├── network/                 # ethclient wrapper: log filters, block-range polling, ERC-20 Transfer decode, Swap decode
│   ├── notify/                  # Provider interface + Noop default (mirrors wallet-backend/internal/notify, not imported cross-module)
│   └── engine/
│       ├── models/              # PaymentHistory, TrackedWallet, IndexerCursor (see §3.6)
│       ├── tracking/            # wallet-discovery sweep: mirrors users.UserWallet -> TrackedWallet
│       ├── indexer/             # the actual log-polling + ProcessTransfer/ProcessSwap logic
│       └── backfill/            # per-address from-genesis history reconstruction for newly tracked wallets
└── main_test.go
```

### 3.2 The indexer: two watchers, one canonical writer

Two independent poll loops, both landing on the same `SavePaymentHistory`
function (the one, canonical writer — no second code path is allowed to
insert a `PaymentHistory` row, closing off the kind of drift found in §1.1):

1. **Live watcher** (`engine/indexer`, the `MonitorPaymentStream`
   equivalent) — every ~4s (matching the cadence `wallet-backend`'s own
   `AddressWatcher` already uses), for the block range since the last
   persisted cursor:
   - `eth_getLogs` for every curated ERC-20 token's `Transfer` topic,
     filtered to **either** topic1 (`from`) **or** topic2 (`to`) matching
     any address in the `TrackedWallet` table (chunked into
     address-batches under the RPC's topic-filter size limit).
   - A block-range scan of transaction `value` fields for native ETH
     transfers touching a tracked address (Base RPC nodes support
     `eth_getBlockByNumber` with full transactions in one call; no
     separate trace API is required for a plain value transfer).
   - Each match is classified (plain transfer vs. mint/burn vs. swap, see
     §3.3) and passed to `SavePaymentHistory`.
   - After each successfully processed block range, the cursor (last
     block number) is **persisted for real** — fixing §1.2's central bug.
     A crash or restart resumes from the last saved block, re-processing
     at most one partially-done range (safe, since the writer is
     idempotent — see §3.6's dedup key).
2. **Backfill watcher** (`engine/backfill`, the `MonitorPublicKeyPayment
   Stream`/`TrackUserWallet` equivalent) — when a wallet is newly tracked
   (mirrored from `wallet-backend`'s `users.UserWallet` with no existing
   `TrackedWallet` row), a one-shot job scans that address's full history
   from genesis (or from a configurable "don't bother before this block"
   floor, since a brand-new Base wallet has no pre-existence to backfill)
   and reconstructs its `PaymentHistory` rows before handing off to the
   live watcher. This preserves the original's real "new wallet gets its
   history backfilled once" design intent, on infrastructure that
   actually works (block-range `eth_getLogs`, not a per-account SSE
   stream Base doesn't offer).

Reconnect/error handling explicitly fixes §1.2's gaps: exponential backoff
with jitter (not a bare fixed 5s retry), a bounded worker pool sized to
actual throughput (not a fire-and-forget 200,000-deep channel with no
overflow signal), and `defer recover()` around every poll iteration so a
single bad block/log never kills the process.

### 3.3 Classifying a transaction: plain payment vs. mint/burn vs. swap

- **Plain payment**: a `Transfer` (or native-value transaction) where
  neither side is the zero address and the transaction touches exactly
  one token/asset for the tracked wallet.
- **Mint / burn**: `Transfer(from=0x0, ...)` → `"MINT TOKEN"`;
  `Transfer(..., to=0x0)` → `"BURN TOKEN"` — the direct, simpler EVM
  analog of the original's issuer-address check (§2).
- **Swap**: a transaction containing **two** `Transfer` events for the
  tracked wallet within the same tx hash (one outbound, one inbound, on
  different tokens) — this is the router-agnostic heuristic that works
  regardless of which DEX/router built the swap, matching
  `wallet-backend`'s own swaps component's "the project configures which
  router it wants, this code doesn't pick for it" philosophy (`PLAN.md`
  §2). Where the transaction's target contract emits a recognizable
  `Swap` event (Uniswap-V3-style), that event's token-in/token-out is
  used to build the `"SWAP <A>><B>"` label directly instead of inferring
  it from the two Transfers — belt and suspenders, preferring the
  authoritative signal when it's available.
- **`path_payment_strict_receive` parity note**: because the Base design
  classifies by *pattern* (two transfers, one tx) rather than by
  enumerating specific Stellar operation-type strings, there is no
  equivalent of §1.2's "we forgot to list this one operation type" bug
  class at all — every swap shape that produces the same two-Transfer
  pattern is caught by construction.

### 3.4 Wallet tracking

`TrackedWallet` is upserted properly this time — a single
`ON CONFLICT (public_key) DO UPDATE` (or GORM's `clause.OnConflict`) call,
not delete-then-recreate (§1.2's dead-code bug). A lightweight sweep
(`engine/tracking`) polls `wallet-backend`'s `users.UserWallet` table for
rows not yet mirrored, exactly mirroring the original's `tracked = 0`
sweep intent, without the churn bug.

### 3.5 Real-time delivery (new capability, not a port of anything that worked)

The original's real-time path was permanently broken (§1.6) — there is
nothing functional to port. Since this engine is uniquely positioned to
observe a payment landing on-chain **regardless of which channel
submitted it** (self-submitted through `wallet-backend`, received from an
arbitrary external sender, or submitted via the servicelinks partner
API), it is the correct place to originate a "payment received/sent" push
notification — something `wallet-backend`'s own payments component cannot
do today for *incoming* payments, since it only knows about transactions
it itself built. This engine calls a small local `notify.Provider`
interface (mirroring `wallet-backend/internal/notify`'s own
Provider-interface-plus-Noop-default pattern, kept as an independent
package rather than a cross-module import so the two services stay
independently deployable) on every newly-recorded `PaymentHistory` row
touching a tracked wallet. Wiring this to a real push vendor (or to
`wallet-backend`'s own notification channel via a small internal HTTP
callback / message queue) is a deliberate later decision, not assumed
here — see the roadmap's explicit "planned, not required for v1" tag.

### 3.6 Data model (final)

```go
type PaymentHistory struct {
    ID                   string    `gorm:"primaryKey"` // uuid
    TransactionHash      string    `gorm:"size:66;not null;index"`
    LogIndex             uint      // 0 for a native-value transfer with no log
    BlockNumber          uint64    `gorm:"not null;index"`
    TransactionDate      time.Time `gorm:"index"`
    TransactionType      string    `gorm:"size:32;not null"` // PAYMENT | MINT | BURN | SWAP <A>><B>
    From                 string    `gorm:"size:42;not null;index"` // EVM address, always set (no alias/name denormalization - joined at read time)
    To                   string    `gorm:"size:42;not null;index"`
    TokenAddress         *string   `gorm:"size:42"` // nil = native ETH
    AssetCode            string    `gorm:"size:12;not null"`
    Amount               string    `gorm:"not null"` // base-unit decimal string, never a float
}
// One canonical unique key, decided once, not two disagreeing copies (fixes §1.1):
//   UNIQUE (TransactionHash, LogIndex) — a native transfer's LogIndex is
//   synthesized as 0 (a transaction can only ever have one native-value
//   transfer), so this stays a true dedup key for both transfer kinds.

type TrackedWallet struct {
    Address string `gorm:"primaryKey;size:42"`
    UserID  uint   `gorm:"index"` // wallet-backend's users.User.ID
}

type IndexerCursor struct {
    ID          uint `gorm:"primaryKey"`
    LastBlock   uint64
}
```

**Deliberate simplification versus the original**: no `From`/`To` alias
denormalization (`"Name[alias]"` baked into the row) — the original's
`PaymentHistoryJSON.From`/`.To` strings are reconstructed at *read* time
by joining `TrackedWallet`/`users` on the raw address instead, so a
username change is reflected in historical rows automatically rather than
requiring a backfill (the original's denormalized copy would show a
user's *old* alias forever on every historical row after a rename — a bug
class this design avoids by construction, not a bug found in the source,
but worth stating as a deliberate improvement).

## 4. Bugs found in the original — fixed, not reproduced

1. **Cursor persistence was designed but never wired up** (`SaveLastCursor`
   dead-code call) — this port's cursor save happens for real, after every
   successfully processed block range (§3.2).
2. **`path_payment_strict_receive` silently produces no history row**, in
   both source repos independently — moot by construction on Base, since
   swap classification is pattern-based, not an enumerated operation-type
   switch (§3.3).
3. **Two repos disagree on `PaymentHistory`'s own unique key** — one
   canonical schema, defined once, in this repo, is the only place
   `PaymentHistory` is declared (§3.6).
4. **`TrackUserWallet` deletes before checking existence**, making its own
   "update" branch permanently dead code — replaced with a real upsert
   (§3.4).
5. **No backoff/jitter/panic-recovery on the streaming loop** — this
   port's poll loop has all three (§3.2).
6. **A wrong-type Horizon type assertion panics the whole process** on the
   first `change_trust` operation seen (`trovo-wallet-monorepo/backend`'s
   `websockets.go`) — moot on Base (no trustline concept to mishandle at
   all), but the general lesson (never blind-assert a decoded event's
   type; always check first) is carried into `engine/indexer`'s event
   decoding.
7. **The read-side `ORDER BY` column comes straight from an unvalidated
   query parameter** (`trovo-wallet-monorepo/backend`'s `history.go`) — out
   of scope for this repo (that code lives in `wallet-backend`), but
   flagged here explicitly as a required fix wherever `wallet-backend`'s
   own read API is extended to serve this engine's richer table (§7).

## 5. Explicitly dropped or deferred (documented, not silently skipped)

- **Market-making trade-fee collection** (`ProcessTrade`, §1.4) — a
  market-maker fee-skimming bot bundled into the same original binary for
  no architectural reason. `wallet-backend`'s `market` component (§4.8 of
  its own `PLAN.md`, already built) is the correct home for any Base
  equivalent; not duplicated here.
- **Stellar memo** — no protocol equivalent on Base; dropped (§2).
- **Temp-account/trustline tracking** — no protocol equivalent; dropped
  (§2).
- **A second dedicated database purely for engine bookkeeping** — folded
  into the one shared Postgres instance, per `wallet-backend/PLAN.md`
  §11's own prior recommendation (§2).
- **Fee/VAT-labeled transaction types** — the original's fee/VAT-skimming
  payment machinery (`pay.go`) has no counterpart in `wallet-backend`'s
  `payments` component (plain transfers only; fee flows that do exist —
  tokenization, patron — already have their own dedicated fee-wallet
  bookkeeping in their own components) — no new transaction-type taxonomy
  is invented here to cover a feature that was never ported upstream.
- **Real-time push wiring to a specific vendor** — the *hook point* is
  designed (§3.5), but which push provider it calls is left as a v2
  decision, matching every other optional-vendor integration's "Noop
  default, real implementation swapped in later" pattern established
  throughout `wallet-backend`.

## 6. Coordination note for `wallet-backend` (not implemented by this plan)

`wallet-backend`'s own `payments.Service.SubmitPayment` currently writes
its **own** narrow `PaymentHistory` row at submit time — capturing only
payments it itself built and submitted, missing anything received from an
external sender or submitted through another channel entirely (the exact
gap a proper chain-indexer exists to close). Once this engine ships, the
correct end-state is: this engine's table becomes the single source of
truth for payment history, `wallet-backend`'s own submit-time write is
removed, and `wallet-backend`'s existing `payments.Service.History`/
`GET .../payments` read path is pointed at this engine's schema (§3.6)
instead. That is a small, separate change **inside `wallet-backend`**,
tracked here as a dependency this plan creates but does not implement —
raise it as its own follow-up when implementation of this engine begins.

## 7. Phased build roadmap

| Phase | Scope | Depends on |
|---|---|---|
| 1 | Module scaffold: `go.mod`, `sharedconfig`, `db` (shared Postgres connection), `network` (ethclient wrapper, curated-token list reuse from `wallet-backend`'s `assets.CuratedToken` table) | — |
| 2 | Data model (§3.6) + migrations | Phase 1 |
| 3 | Wallet tracking sweep (`engine/tracking`, §3.4) | Phase 2 |
| 4 | Live indexer: Transfer-log + native-value polling, transaction classification (§3.2, §3.3), real cursor persistence | Phase 3 |
| 5 | Backfill watcher for newly tracked wallets (§3.2.2) | Phase 4 |
| 6 | Reconnect/backoff/panic-recovery hardening (§3.2's explicit fixes) | Phase 4 |
| 7 | Real-time push hook (`notify.Provider`, Noop default) — planned, not required for a working v1 | Phase 4 |
| 8 | Full integration pass: build/vet/test, a live Base Sepolia smoke test against a handful of tracked test wallets, README | Everything above |
| 9 (tracked, not implemented here) | `wallet-backend` coordination change (§6) | This engine's Phase 4+ |

Each phase, when implementation is authorized, follows this project's
established discipline: build → `go build`/`vet`/`gofmt`/`test` clean →
document in this file's own "Implementation notes" → commit and push.

## 8. Implementation notes

Phases 1-8 are implemented as designed above, with the following notes:

- **Wallet-tracking topic filter** (§3.2): implemented as
  `network.Client.TransferLogsTouchingWallets`, which issues two
  `eth_getLogs` calls per tracked-address batch (one with the batch in
  topic1/`from`, one in topic2/`to`) and merges/dedupes by
  `(TxHash, LogIndex)`, since a single `eth_getLogs` call ORs *within* one
  topic position but ANDs *across* positions - there is no single-query
  way to express "`from` in set OR `to` in set". Address batches are
  capped at 200 per call (`trackedAddressBatchSize`) to stay under RPC
  topic-array size limits.
- **Swap classification** (§3.3): implemented as the router-agnostic
  "two Transfer events in one tx, same tracked wallet outbound on one
  token and inbound on another" heuristic. The "belt and suspenders"
  authoritative Uniswap-V3-style `Swap` event decode mentioned in §3.3 as
  a secondary signal is **not implemented** in this pass - the heuristic
  alone satisfies the classification requirement, and decoding a specific
  router's `Swap` event shape is deferred until a specific router is
  chosen for the wallet app, rather than guessed at here.
- **Live watcher poll range cap**: `RunOnce` caps each poll to
  `maxBlocksPerPoll` (2000) blocks, so a long gap since the last saved
  cursor (e.g. a restart after extended downtime) catches up in bounded
  chunks across successive polls rather than one unbounded
  `eth_getLogs`/block-scan call.
- **Test coverage**: `main_test.go` covers `SavePaymentHistory`'s
  idempotency (the property the whole crash-safe restart design leans
  on) and that two distinct log indexes in the same transaction hash are
  both kept (the swap case). `internal/engine/tracking/tracking_test.go`
  covers the upsert fix itself (§1.2/§4 finding 4): sweeping the same
  `UserWallet` row twice upserts in place and reports it as newly tracked
  only once.
- **Live Base Sepolia smoke test**: this development sandbox's egress
  policy blocks outbound connections to `sepolia.base.org` (confirmed via
  a 403 at the egress gateway), so the live RPC path itself could not be
  exercised end-to-end here. What *was* verified in this environment: the
  engine builds/vets/tests clean, boots, opens and migrates its own three
  tables against SQLite, and runs both background loops indefinitely
  without crashing when the RPC endpoint is unreachable (exponential
  backoff with jitter, alerting via the configured `Notifier`, no panic).
  Verify the live `eth_getLogs`/block-scan path against real Base Sepolia
  traffic in an environment with outbound RPC access before deploying.
- **Real-time push hook** (§3.5, Phase 7): `notify.Provider` and
  `NoopProvider` are implemented and wired into `SavePaymentHistory`,
  which notifies whichever tracked side(s) of a newly-recorded transfer
  are known. `main.go` wires the `NoopProvider` by default, matching
  §3.5's "planned, not required for v1" tag - swapping in a real
  provider (a push vendor, or a callback into `wallet-backend`) is a
  deliberate later decision, not assumed here.
