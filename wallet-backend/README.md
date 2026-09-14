# wallet-backend

A full port of the Trovo Wallet API to **Base** (Coinbase's OP-Stack
Ethereum L2). See [`PLAN.md`](./PLAN.md) for the full architecture
rationale, the subsystem-by-subsystem port plan, and what's implemented so
far vs. still planned - this README is the practical "how do I run it"
companion for what exists today.

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
- [go-qrcode](https://github.com/skip2/go-qrcode) for shortlink QR code generation

## Project layout

Each component under `internal/components/` is a vertical slice
(`controllers/` → `services/` → `models/`) that registers its own routes via
an `Init(router, gc)` function called from `main.go`. `sharedconfig.GlobalConfig`
is the single dependency-injection struct threaded through every component -
see `internal/sharedconfig/config.go`.

```
internal/
├── sharedconfig/   GlobalConfig (DI struct) + Env loader
├── db/             OpenDB (postgres/sqlite), MigrateDB, connection-pool limits/stats
├── cache/          Cache interface + Redis impl + no-op
├── network/        Base/EVM integration - the only package importing go-ethereum
├── middleware/      CORS, per-request signature auth + admin JWT auth, servicelinks API-key auth
├── cryptoutil/       secp256k1 key derivation, AES-GCM, bcrypt, hashing
├── apperrors/        GenericError + typed constructors
├── validators/       format validators (EIP-55 address checks, etc.)
├── notify/           Mailer / SMSProvider / PushProvider + default impls
├── storage/          Blob interface (Put/Get/Delete) + local-disk impl
├── kyc/              Provider interface + ManualKYCProvider stub
├── fiat/             Processor interface (no default impl - see doc comment)
├── rates/            Provider interface + static/fixture impl
├── alerting/         Notifier interface + Discord webhook impl
├── geoip/            Provider interface + NoopProvider + ipapi.co impl
├── contracts/        embedded Solidity ABI/bytecode + deployment helpers
└── components/
    ├── root/          health check reporting the configured chain ID
    ├── users/          registration, profile, security questions/recovery
    ├── assets/         curated-token catalog, balance lookup, allowance build/submit
    ├── payments/       build/submit native or ERC-20 transfer, payment history
    ├── swaps/          generic, router-address-configurable DEX call builder
    ├── sharedaccess/   multi-party wallet access: propose/approve/execute by threshold
    ├── announcements/  in-app announcements (public read, JWT-admin write)
    ├── callbacks/      generic webhook receiver stub
    ├── kyc/            Sumsub + Doja identity verification
    ├── fiat/           fiat payment invoices + starter-gas/reward-token activation
    ├── stablerail/     NGN on/off-ramp onboarding, virtual accounts, withdrawals
    ├── crypto/         OneLiquidity-backed crypto deposit crediting/withdrawal
    ├── market/         off-chain order-book market making with escrow settlement
    ├── tokenization/   real-world-asset tokenization: apply, vet, mint, buy, exit
    ├── patron/         subscription/membership tiers
    ├── servicelinks/   API-key-authenticated third-party partner surface
    ├── reference/      country catalog/config, dynamic client-form definitions
    └── shortlink/      self-hosted short links + QR codes
```

See `PLAN.md` §4 for the full design rationale behind each component and
§10 for the phase-by-phase build roadmap (every phase through 14 is
**DONE**).

## Auth

Three independent auth mechanisms, each scoped to its own surface so a
credential for one can never be replayed against another:

- **Primary API** (most `/v1/...` routes across every component - users,
  assets, payments, swaps, shared-access, tokenization, patron, and more):
  a stateless per-request signature, not a session token - there is no
  login step and nothing to expire or revoke.

  1. Build the message `fullPathWithQuery + signerAddress + timestamp`
     (the exact path and query string being requested, the signer's own
     address, and the current Unix timestamp in seconds).
  2. Sign it with the wallet's `personal_sign` (EIP-191).
  3. Send four headers on the request itself:
     `X-Signer-Address` (who signed), `X-Wallet-Address` (which wallet
     the request acts on - the same address for a self-service call, a
     different one when delegated via shared access), `X-Signature`, and
     `X-Timestamp`.
  4. The server verifies the signature fully offline
     (`cryptoutil.VerifyPersonalSign`, no database or cache lookup) and
     rejects the request if `X-Timestamp` has drifted from the server's
     clock by more than `SIGNATURE_AUTH_TOLERANCE_SECONDS` - the only
     replay defense this scheme has, since there's no session or nonce
     to invalidate.

  Since every user's primary wallet is a Gnosis Safe smart-contract
  account rather than the signer's own EOA (`PLAN.md` §13.2), `signer` and
  `wallet` are ordinarily *different* addresses even for a self-service
  call: `POST /v1/users` (registration) computes and returns the primary
  wallet's Safe address from the signer key you register with, and every
  later self-service call names that Safe address as `X-Wallet-Address`
  while continuing to sign with the same EOA as `X-Signer-Address`. A
  request is authorized if the signer *is* that wallet's registered
  owner (`User.SignerAddress`), *equals* the wallet address outright (the
  bare, undeployed identity an account-recovery Branch A swap produces -
  see "Account recovery" below), or holds a shared-access role on that
  wallet - see `internal/middleware/signature_auth.go` and `PLAN.md` §12
  and §13.

  A freshly registered primary wallet is a *counterfactual* Safe: its
  address is known and can receive funds immediately, but nothing can be
  executed from it until it's actually deployed on-chain via
  `POST /v1/users/wallet/deploy` (idempotent - safe to call more than
  once). See `PLAN.md` §13.11 for exactly which operations require this
  first.

- **Admin surface** (`/v1/admin/...` across every component - see
  "Admin surface" below): a separate-audience Bearer JWT issued via
  `middleware.IssueToken(..., middleware.AudienceAdmin, ...)` (wire up a
  real admin login flow before shipping this to production - none is
  included in the base template). This token can never be used against
  any other route - see the `Audience*` constants in
  `internal/middleware/jwt_auth.go`.

- **Servicelinks partner API** (`/v1/partner/...` - see "Servicelinks
  partner API" below): a SHA-256-hashed API key sent as `X-API-Key`,
  resolved and status-checked once by `middleware.APIKeyAuth` and scoped
  per-route by eleven granular capability flags.

## Shared/multi-party wallet access

A group of members controls one Base wallet via a threshold of off-chain
approvals rather than any single private key - see `PLAN.md` §2 and §4.2
for the full design and its tradeoffs versus a smart-contract wallet.

1. `POST /v1/shared-access/groups` with `{"name", "threshold", "members": [{"address", "role"}]}`
   (`role` is `INITIATOR`, `APPROVER`, or `VIEW_ONLY`) → creates the group
   and derives its Base address.
2. An `INITIATOR` proposes an action: `POST /v1/shared-access/actions` with
   either `{"groupId", "kind": "payment", "recipient", "tokenAddress", "amount"}`
   or `{"groupId", "kind": "swap"|"contract_call", "to", "valueWei", "data"}`.
3. Any `APPROVER` fetches `GET /v1/shared-access/actions/:id` to get the
   exact `messageToSign`, signs it with `personal_sign`, and calls
   `POST /v1/shared-access/actions/:id/approve` with `{"signature"}`.
4. Once the group's `threshold` of distinct approvers has signed, the
   server automatically derives the group's key and executes the
   transaction - no further action needed. If execution fails (e.g. a
   transient RPC error), approving again with the same signature retries it.
5. An `APPROVER` may instead `POST /v1/shared-access/actions/:id/reject`
   with `{"reason"}`.

`GET /v1/shared-access/actions` lists pending actions for the caller's
groups; `GET /v1/shared-access/balance/:groupId` (optional `?token=`)
checks the group wallet's balance.

## Account recovery

A bare EVM address can't be "re-keyed" the way Stellar's native multi-sig
recovery re-keys an account - an address *is* its key. So what's
implemented today re-points a username to a new, caller-supplied address
once two factors prove the caller is who they claim to be: every
configured security answer, plus a one-time code emailed to the account's
registered address. See `PLAN.md` §4.3 for the full design.

A second mechanism - true wallet recovery, preserving the *same* address
and its sub-wallets/shared-access memberships via a Safe owner-swap once
the primary wallet is a Safe (`PLAN.md` §13) - is planned but not yet
implemented; see `PLAN.md` §15 for the full design of both this
mechanism (kept as-is, "Branch A") and that one ("Branch B") coexisting
side by side.

1. A logged-in user opts in once: `POST /v1/users/account-recovery` (and
   `DELETE /v1/users/account-recovery` to opt back out) - both require a
   signed request (`middleware.SignatureAuth`), and require security
   answers to already be set via `POST /v1/users/security-answers`.
2. Recovery itself is deliberately **unauthenticated** - its entire point is
   helping someone who can no longer produce a signature at all:
   `POST /v1/account-recovery/:username/request-otp` always responds `204`
   regardless of whether the username exists or has recovery enabled, so it
   can't be used to enumerate accounts.
3. `POST /v1/account-recovery/:username/recover` with
   `{"newAddress", "newAddressSignature", "otp", "answers": [{"securityQuestionId", "answer"}]}`
   (every configured question must be answered, not a subset).
   `newAddressSignature` is a `personal_sign` signature, produced by
   `newAddress`'s own key, over the exact string
   `wallet-backend account recovery\nusername: <username>\nnew address: <newAddress>`
   - proving the caller controls that address's key, the same non-custodial
   guarantee `Register` enforces, so recovery can never attach a
   username to an address no one can actually sign from. On success this
   re-points the username's `User.Address` and primary `UserWallet.Address`
   to `newAddress` in one transaction, and revokes any shared-access group
   memberships the old address held (other members must manually re-invite
   the recovered account once satisfied the recovery is legitimate, rather
   than the server silently trusting it).
4. Every completed recovery is logged to `AccountRecoveryLog`, including an
   EIP-191 signature over the change from a server-side recovery-authority
   key (`RECOVERY_AUTHORITY_SALT`) - a tamper-evident attestation standing
   in for the on-chain co-signature Stellar's native flow used, since
   re-pointing a username is a purely application-level change with nothing
   to co-sign on-chain.

A small blocklist of reserved usernames (`ReservedName`, seeded on first
boot) is checked at registration so no one can register `admin`, `support`,
and similar staff/brand-impersonating handles.

## KYC

Identity verification against the same two vendors the original project
used - Sumsub and Dojah - entirely chain-agnostic, so this is close to a
line-for-line port; see `PLAN.md` §4.4 for the three deliberate deviations
from the original (dropped client-self-approval endpoint, fixed both
webhooks' signature verification, deferred push/Stablerail hooks).

- **Sumsub** (levels enforced in order 1 → 2 → 3):
  1. `GET /v1/kyc/sumsub/levels` lists the configured level names.
  2. `POST /v1/kyc/sumsub/initiate/:levelName` creates a Sumsub applicant
     and returns `{"applicantToken", "applicant"}` - the client hands
     `applicantToken` to Sumsub's own mobile/web SDK to run document and
     liveness capture.
  3. `GET /v1/kyc/sumsub/progress` returns the caller's per-level
     initiated/done flags.
  4. Sumsub reports the outcome asynchronously to
     `POST /v1/callbacks/kyc/sumsub/webhook` (HMAC-SHA256-verified via the
     `x-payload-digest`/`x-payload-digest-alg` headers against
     `SUMSUB_SECRET_KEY`); a `GREEN` review marks that level done and
     raises `User.KYCVerifiedLevel`, a `RED` review resets it for retry.
- **Dojah** (widget-based, BVN-centric, up to 4 levels):
  1. `GET /v1/kyc/doja/widgets` lists widget IDs configured in the
     `DojaWidget` table (an operator's own Dojah-dashboard widget IDs -
     none are seeded, since they're specific to whoever owns the account).
  2. `GET /v1/kyc/doja/progress` returns the caller's per-level
     submitted/completed flags.
  3. Dojah posts progress to `POST /v1/callbacks/kyc/doja/webhook`
     (HMAC-SHA256-verified via the `x-dojah-signature` header against
     `DOJA_SECRET_KEY`); `Completed` raises `User.KYCVerifiedLevel` to that
     widget's level (never lowering a level already granted by the other
     vendor), `Failed` resets that level's progress.

`User.KYCVerifiedLevel` is the single field either vendor raises - a future
phase gating a feature on KYC (fiat activation limits, tokenization
purchase limits) should read this field regardless of which vendor a given
user verified through.

## Fiat payments & activation

A one-time paid "activation" flow that dispenses starter ETH gas and a
reward ERC-20 token once a new user pays a configured fiat amount, plus a
generic decoupled invoice pattern any future on-chain-settling fiat
payment (e.g. a Phase 9 asset purchase) can reuse unchanged; see
`PLAN.md` §4.5 for the full design and its two scope trims (a single
global activation price rather than per-country, and a payment-type-
agnostic settlement path rather than a Tokenization-specific one).

- `GET /v1/fiat/activate` (authed) returns the current price:
  `{"alreadyActivated", "fiatAmount", "fiatCurrency", "rewardTokenPercent", "gasPercent"}`.
- The client pays directly via Flutterwave's own checkout SDK, using a
  client-chosen idempotency reference as Flutterwave's `tx_ref`.
- Flutterwave confirms the charge to
  `POST /v1/callbacks/fiat/flutterwave/webhook` (verified via the
  `verif-hash` header against `FLUTTERWAVE_SECRET_HASH`, constant-time
  compared). For `meta_data.product == "activation"`, this derives the
  starter-gas/reward-token split from `ActivationConfig`, converts each
  half from fiat to on-chain amounts via the existing `rates.Provider`,
  and dispenses both from a `FAUCET_KEY_SALT`-derived key - guarded by
  `User.Activated` so a duplicate webhook delivery can never double-pay.
  Any other product settles through the generic
  `POST /v1/fiat/flutterwave/invoices` → webhook → submit-signed-tx path
  (`SettlePendingInvoice`), which doesn't care what the payment is for.
- `GET /v1/fiat/payments` (authed) lists the caller's completed payments
  and invoices.

## Stablerail (NGN on-ramp)

A BVN-gated NGN-to-stablecoin deposit rail. Unlike every other external
integration in this port, Stablerail's own API is already close to
chain-agnostic - it takes a destination wallet address and a network code
per request and performs the actual on-chain transfer on its own
infrastructure, so this component has no `network.Client`/on-chain code
at all; see `PLAN.md` §4.6.

1. `POST /v1/stablerail/onboard/:bvn` (authed, 11-digit BVN) starts
   identity verification with Stablerail - this also happens
   automatically once Dojah's KYC webhook reports a completed BVN-type
   Level 1 verification (see the KYC section above), via a callback
   `main.go` wires from `kyc.Service.OnBVNVerified`.
2. `GET /v1/stablerail/banks` (authed) lists Stablerail's synced
   supported-bank list.
3. `POST /v1/stablerail/onramp/:amount` (authed, NGN amount) - once
   onboarding has completed - creates a deposit request and returns the
   virtual account (`accountNumber`, `bankName`, `accountName`) to pay
   into.
4. A background poller checks pending onboarding/on-ramp requests against
   Stablerail's status endpoints every 10 seconds; once a deposit is
   funded, it automatically triggers Stablerail's own withdrawal of the
   converted stablecoin to the user's Base address - no client action
   needed. A separate poller re-syncs the supported-bank list every 10
   minutes.

Set `STABLERAIL_ENABLED=true` and `STABLERAIL_API_KEY` to turn this on;
every route and the KYC-triggered hook return a `202` "not enabled"
response otherwise.

## Crypto deposit/withdrawal (OneLiquidity)

External-crypto deposit addresses and withdrawal, backed by OneLiquidity -
see `PLAN.md` §4.7 for the full design, including the deliberate
deposit-crediting choice (a treasury **transfer**, not the original's
Stellar **mint** - Base's curated assets are ordinary ERC-20 contracts
this project doesn't control minting for) and the one documented gap
(shared-access multi-party withdrawal isn't wired up yet).

1. `GET /v1/crypto/deposit-address/:currency` (authed) returns the
   caller's deposit addresses, requesting a new OneLiquidity subwallet on
   first call.
2. A background poller checks OneLiquidity's deposit list every 60
   seconds; a newly completed deposit is credited automatically by
   transferring the matching `CuratedToken` from a derived treasury
   address (`CRYPTO_TREASURY_KEY_SALT`) to the depositing user - an
   operator funds that address with each curated token ahead of time. A
   deposit in a currency with no curated token is recorded but left
   uncredited rather than failing.
3. `GET /v1/crypto/deposit-history` (authed) lists the caller's deposits
   and their crediting status.
4. `GET /v1/crypto/withdrawal-networks/:currency` (authed) returns cached
   per-network min/max/fee limits plus the treasury address a withdrawal
   must transfer to (see the next step).
5. `POST /v1/crypto/withdrawals` (authed) with
   `{"currency", "network", "toAddress", "amount", "signedTreasuryTransferTx"}` -
   the client first builds and signs a transfer of `amount` from their own
   Base address to the treasury address, then this backend submits that
   transaction as proof of the debit before asking OneLiquidity to pay the
   external network. Only single-owner withdrawal is implemented; a
   shared-access group withdrawing external crypto is a documented gap,
   not a silent drop (see `PLAN.md` §4.7).
6. `GET /v1/crypto/withdrawal-history` (authed) lists the caller's
   withdrawal requests and their status.

## Market making (off-chain order book)

A server-matched limit-order book, standing in for placing a resting
offer directly on Stellar's protocol-level DEX - Base has no equivalent,
so this needed a real design choice (see `PLAN.md` §4.8 and §11 for why
off-chain-matched was chosen over dropping resting orders entirely).

Settlement pulls each matched side's asset via `transferFrom` against a
prior `approve()` of a derived escrow address, rather than the server
ever holding funds itself - the same off-chain-order/on-chain-settlement
pattern the 0x Protocol popularized, necessary because Base wallets are
non-custodial and the server can't move either side's asset without a
signature it doesn't have.

1. `GET /v1/market/escrow-address` (public) returns the address a maker
   must `approve()` before placing an offer - for the base asset if
   selling, for `quantity * pricePerUnit` of the quote asset if buying.
2. `POST /v1/market/offers` (authed) with
   `{"offerType": "BUY"|"SELL", "baseToken", "quoteToken", "pricePerUnit", "quantity"}`
   places a resting limit order and immediately tries to match it,
   price-time priority, against the book - repeatedly, until it's fully
   filled or no more crossing offers remain. A match always executes at
   the **resting** (older) offer's price. Only curated ERC-20 pairs can
   be traded - native ETH has no `approve`/`transferFrom` concept.
3. `GET /v1/market/orderbook?baseToken=...&quoteToken=...` (public) and
   `GET /v1/market/trades?baseToken=...&quoteToken=...` (public) return
   the live book and trade history for a pair.
4. `GET /v1/market/offers` (authed) lists the caller's own offers;
   `DELETE /v1/market/offers/:offerId` (authed) cancels one that's still
   `OPEN` or `PARTIALLY_FILLED`.

A known limitation, documented rather than hidden: settling a match is
two independent `transferFrom` calls, not one atomic operation, so a
second leg failing after the first already cleared cancels the failing
side without automatically making the other side whole - the honest cost
of not yet having an atomic on-chain escrow contract (a natural fit for
Phase 8's on-chain infrastructure).

## On-chain infrastructure (contracts, price reading, cache invalidation)

Base has no protocol-level asset issuer, order book, or operation stream
the way Stellar/Horizon does, so this port adds the on-chain building
blocks those features are built from (`PLAN.md` §5):

- **Contracts** (`internal/contracts/solidity/`) — `TokenizedAsset.sol`
  (an `ERC20Burnable`+`Ownable` token with an owner-only `mint`, used once
  per tokenized asset — see the upcoming tokenization phase) and
  `Sale.sol` (an `Ownable` primary-sale contract with `buy`/`setPaused`/
  `withdrawUnsold`, so a purchase is one atomic on-chain call instead of
  a bespoke multi-transaction dance). Both are compiled ahead of time
  with solc 0.8.24 — never at runtime — and their ABI+bytecode is checked
  in under `internal/contracts/artifacts/` and embedded into the binary
  via `//go:embed`; see `internal/contracts/solidity/README.md` for the
  exact toolchain and how to reproduce the build after editing a
  `.sol` file. `internal/contracts/contracts.go` exposes Go helpers to
  build deployment calldata and encode each contract's methods, and
  `network.Client.DeployContract` submits a deployment the same way every
  other transaction in this codebase is built (a hand-rolled
  `types.DynamicFeeTx` with `To: nil`), not via
  `go-ethereum/accounts/abi/bind`'s generated bindings.
- **On-chain price reading** (`internal/network/price.go`) —
  `GetPoolState` reads a Uniswap V3 pool's `slot0`/`token0`/`token1`, and
  `PoolPrice` converts the pool's `sqrtPriceX96` into a human price
  adjusted for both tokens' decimals. This is the direct substitute for
  Stellar's protocol-level DEX price endpoints (`PLAN.md` §2).
- **Chain-log polling for cache invalidation**
  (`internal/network/watcher.go`) — `AddressWatcher` polls `eth_getLogs`
  for `Transfer`/`Approval` events over the block range since its last
  pass (main.go runs this roughly every two Base blocks, ~4s) and fires a
  one-shot callback for each watched address a matching log touches. This
  replaces Horizon's operation/effect streaming, which the original used
  only to invalidate caches when something changed for an account
  (`PLAN.md` §2). `GET /v1/users/:username` is wired up as the first
  caller: a cache hit is served straight from Redis (backed by a 5-minute
  TTL in case a poll is ever missed), and a cache miss registers the
  user's address with the watcher so the entry is dropped the moment a
  Transfer or Approval touches it. Any future endpoint that caches
  something keyed by an address can reuse the same
  `GlobalConfig.AddressWatcher` instead of building its own invalidation
  path.

Deploying and calling these contracts end-to-end needs a real (or
in-process simulated) EVM, which isn't reachable in every environment
this code is developed in — see the doc comments in
`internal/contracts/contracts_test.go` and `internal/network/price_test.go`
for why those packages test ABI encode/decode round-trips and price math
directly instead of against a live chain, the same posture this port
takes for every other vendor/RPC integration without local credentials.

## Tokenization

Representing and trading a tokenized real-world asset — the largest
subsystem in this port. See `PLAN.md` §4.9 for the full design; this
section covers the shape a caller actually sees.

**Lifecycle**: `DRAFT → APPLICATION_CONFIRMED → FEE_CONFIRMED →
FEE_ACKNOWLEDGED → MINTED → PRIMARY_SALE_ACTIVE → SECONDARY_SALE_ACTIVE`.

1. `POST /v1/tokenization` (authed) creates or updates a Draft application
   — requires KYC. `PUT /v1/tokenization/:assetId/confirm` charges the
   application fee and moves to `APPLICATION_CONFIRMED`.
2. `PUT /v1/admin/tokenization/:assetId/vet` (staff) selects the asset's
   stakeholders (custodian, manager, issuing house, legal partner, rating
   agency, trustee) and snapshots their fees.
   `POST /v1/tokenization/:assetId/fee/confirm` (authed, requires vetting)
   moves to `FEE_CONFIRMED`; `POST /v1/admin/tokenization/:assetId/acknowledge-fee`
   (staff) moves to `FEE_ACKNOWLEDGED`.
3. `POST /v1/tokenization/:assetId/mint-request` (a global minting
   approver/initiator) opens a mint request requiring signoff from the
   asset's own `MintingApprovers` list (≥4 addresses, threshold =
   `len(approvers)-2`). Each approver signs
   `GET /v1/tokenization/mint-approvals/:mintApprovalId`'s returned
   message and posts it to
   `POST /v1/tokenization/mint-approvals/:mintApprovalId/sign`. Once the
   threshold is met, the server derives a per-asset issuer key (contract
   owner) and distribution key (treasury), deploys `TokenizedAsset.sol`
   and `Sale.sol` (Phase 8), mints the reserved and for-sale supply, lists
   the asset as a `CuratedToken`, and flips `Status` to `MINTED`.
4. Once a background worker (main.go, 30s poll) sees `SalesStart` arrive,
   `Status` flips to `PRIMARY_SALE_ACTIVE`. Buyers purchase via:
   - **Crypto**: `POST /v1/tokenization/:assetId/subscribe` builds an
     unsigned `Sale.buy()` call (after the buyer separately `approve()`s
     the quote token); `POST /v1/tokenization/:assetId/subscribe/confirm`
     records it once self-submitted.
   - **Fiat**: `POST /v1/tokenization/:assetId/subscribe/fiat` reuses
     `internal/fiat`'s decoupled invoice pattern unchanged — the server
     signs the distribution-key transfer immediately and hands it to the
     existing Flutterwave invoice/webhook flow, so no buyer signature is
     needed at all (Base has no trustline step to require one for, unlike
     the Stellar original).
   A `PRIVATE` offering (`OfferingType`) additionally requires the buyer
   be a member of the asset's `ClosedGroupID` — a
   `sharedaccess.ClosedGroup` with `Purpose: PRIVATE_OFFERING` (§4.2),
   reused rather than duplicated.
5. Once `SalesEnd` arrives, the worker pauses the `Sale` contract and
   flips `Status` to `SECONDARY_SALE_ACTIVE` — from here the asset trades
   only through the existing off-chain order book (Market making, above),
   which already supports any curated ERC-20 pair.
6. `POST /v1/tokenization/:assetId/early-exit` builds an unsigned
   `burn()` call the holder submits themselves (no server-signed transfer
   needed — `TokenizedAsset.sol` is `ERC20Burnable`);
   `POST /v1/tokenization/:assetId/early-exit/confirm` records the
   NAV-based penalty payout for manual/off-chain settlement, exactly as
   upstream never automated it on-chain either.
7. `POST /v1/tokenization/:assetId/interest` logs a pre-launch waitlist
   entry (only while `Status == MINTED`), notified when the primary sale
   activates.

A large reference-data surface (sectors, custodians, managers, issuing
houses, legal partners, rating agencies, trustees, fees, currencies,
protection options, proceed cycles, allowed countries, country configs) is
available at `GET /v1/tokenization/public` (unauthenticated subset) and
`GET /v1/tokenization/reference` (full, authed).

**Deliberately dropped or simplified versus the original** (all documented
in `PLAN.md` §4.9): no synthetic intermediate "internal balance" quote
currency or multi-hop DEX pathfinding (a purchase settles in exactly the
asset's configured quote currency); no trustline/authorization-flag
concept anywhere; the dormant proceed/payout-schedule engine is ported as
schema only, never wired to a route or worker, matching its actual
(non-functional) state upstream; the partner-API passthrough
(`/v1/trovo-api/assets/...`) is deferred to Phase 11 alongside its API-key
middleware, since the `servicelinks` component it belongs to doesn't exist
yet.

## Patron/membership

A subscription tier system — three fixed packages (`GOLD` < `PLATINUM` <
`DIAMOND`) each sellable at three fixed cadences (`MONTHLY` < `ANNUAL` <
`LIFETIME`), seeded on boot. See `PLAN.md` §4.10 for the full design,
including two activation-worker bugs found upstream and fixed rather than
reproduced.

- `GET /v1/patron/reference` (public) lists packages, tiers, membership
  grades (package+tier → USD price), and accepted payment currencies.
- `GET /v1/patron` (authed) returns the caller's current membership, if any.
- `GET /v1/patron/history` (authed) lists their subscription history,
  including any not-yet-effective queued upgrade.
- `POST /v1/patron/subscribe` (authed) with
  `{"membershipGradeId", "paymentAssetSymbol"}` validates the upgrade is
  actually allowed (you can't move to a grade at or below a still-valid
  higher one) and returns an unsigned payment transaction plus a quote
  (price, VAT, and whether it takes effect immediately or is queued to
  start the day after your current membership expires).
  `POST /v1/patron/subscribe/confirm` (authed) with the same body plus
  `txHash` re-validates, records the subscription, and — only if it takes
  effect immediately — updates the active membership.
- A background worker (main.go, 30s poll) promotes queued upgrades once
  their effective date arrives.

## Servicelinks partner API

An API-key-authenticated surface letting a third-party service act on
behalf of wallet users with scoped permissions. See `PLAN.md` §4.11 for the
full design and a cluster of authorization bugs found in the original and
fixed here (plaintext API keys, missing status checks, an overloaded
permission flag, missing wallet-ownership scoping, a broken KYC-override
check, and an unscoped document store).

Admin (`POST /v1/admin/servicelinks`, JWT `AudienceAdmin`) provisions a
service link with eleven independently-grantable capabilities
(`canLogin`, `canRequestAuthorization`, `canRegisterEvents`,
`canViewUserInfo`, `canSendPushNotifications`, `canLookupTokenInfo`,
`canCreateUsers`, `canUpdateKyc`, `canSendPayments`, `canReadBalances`,
`canManageTokenization`) and returns the raw API key exactly once — only
its SHA-256 hash is ever stored. A new service link starts unverified;
`POST /v1/admin/servicelinks/:id/verify` (and `/suspend`, `/reactivate`)
manage its lifecycle. Every partner-facing route requires
`X-API-Key: <raw key>` and is rejected centrally if the service link is
inactive, suspended, or not yet verified.

Partner routes (`/v1/partner/...`, each gated by its own capability):

- `POST /users` (`canCreateUsers`) onboards a user on the partner's behalf.
- `GET /users/:userId` (`canViewUserInfo`), `PUT /users/:userId/kyc`
  (`canUpdateKyc`), `POST /users/:userId/push` (`canSendPushNotifications`),
  `GET /tokens/:symbol` (`canLookupTokenInfo`).
- `POST /users/:userId/payments/build` and `/submit`, `GET
  /users/:userId/payments`, `GET /users/:userId/balance`, `POST
  /users/:userId/wallets` (`canSendPayments`/`canReadBalances`).
- `GET /tokenization/:assetId`, `POST
  /users/:userId/tokenization/:assetId/purchase/build` and `/record`, `GET
  /users/:userId/tokenization/subscriptions` (`canManageTokenization`,
  thin passthroughs onto the tokenization component).
- `POST /approvals`, `GET /approvals/:id/verify` — the consolidated
  login/authorize/event consent flow (one `ServiceLinkApproval` model with
  a `Kind` discriminator replaces three near-identical flows upstream). A
  `LOGIN`-kind approval, once the named user approves it, redeems into a
  servicelink-session JWT (`middleware.AudienceServiceLinkSession`) for
  that user.
- `POST /documents`, `GET /documents/:id`, `DELETE /documents/:id` — a
  stakeholder-document store scoped to the calling service link's own
  tenant (no ownership record at all upstream — an IDOR).

The end-user side of the approval flow is authenticated the same way as
every other user route, not API-key: `GET /v1/approvals/:id` and
`POST /v1/approvals/:id/approve` require a signed request
(`middleware.SignatureAuth`) matching the approval's target user.

Every route that names a specific user enforces
`user.CreatedByServiceLinkID == callingServiceLink.ID` before acting on
that user's wallet — deny-by-default, including when the field is `nil`
(an organically-registered user), fixing the original's KYC-override check
which skipped this check entirely on `nil`.

## Admin surface

Every admin route is JWT-authenticated with `middleware.AudienceAdmin` (a
separate token audience from every other JWT this API issues, so an admin
token can never be replayed against another route or vice versa - and
user operations don't use a JWT at all, see "Authentication" above). See
`PLAN.md` §4.12.

- Tokenization vetting/minting/fee-acknowledgement/sales-date-management/
  deletion — `/v1/admin/tokenization/...` (built in Phase 9, §4.9).
- Servicelinks provisioning/verification/suspension —
  `/v1/admin/servicelinks/...` (Phase 11, §4.11).
- KYC config — `/v1/admin/kyc/sumsub/levels[/:id]` and
  `/v1/admin/kyc/doja/widgets[/:id]` manage the Sumsub-level and
  Doja-widget-ID catalogs an operator keeps in sync with their vendor
  dashboards.
- Patron config — `/v1/admin/patron/packages[/:id]`,
  `/v1/admin/patron/tiers[/:id]`, `PUT /v1/admin/patron/grades` (create or
  reprice a package+tier's USD price), and
  `PUT /v1/admin/patron/payment-assets/:symbol` manage the subscription
  package/tier/pricing catalog and payment-currency allow-list.
- Wallet lookup — `GET /v1/admin/wallet/:address/balance` looks up any
  address's native/ERC-20 balance (the same `assets.Service.Balance` the
  public/authed balance routes use, without the per-caller address
  restriction).
- Reference data — `PUT /v1/admin/reference/countries/:code[/config]` and
  `PUT`/`DELETE /v1/admin/reference/forms/:id` manage the country catalog
  and dynamic client-form definitions (Phase 13, §4.13).

## Reference data, geo-IP risk, and shortlinks

See `PLAN.md` §4.13.

- `GET /v1/reference/countries`, `GET /v1/reference/countries/:code/config`,
  `GET /v1/reference/forms[/:id]` (all public) expose the country catalog,
  per-country config (fiat activation pricing, regulator info, a
  `highRisk` flag), and versioned dynamic form definitions a client can
  render without an app-store release.
- Registration risk: if `GEOIP_BASE_URL` is set, every `POST /v1/users`
  registration resolves the caller's IP to a country via
  `internal/geoip.IPAPIProvider` and records `registrationCountryCode` /
  `registrationHighRisk` (true only if that country has a
  `reference.CountryConfig` row with `highRisk: true`) on the new user -
  purely advisory fields for downstream review; a lookup failure never
  blocks registration. Unset (the default), registration behaves
  identically via `geoip.NoopProvider`.
- Discord alerting (`DISCORD_WEBHOOK_URL`) now also fires on a rejected
  payment/swap submission, a failed or low-balance activation-faucet
  dispense (`FAUCET_LOW_BALANCE_THRESHOLD_ETH`, checked every 30 minutes
  when set), and a saturated/exhausted database connection pool (checked
  every minute against `DB_MAX_OPEN_CONNS`).
- Shortlinks (`internal/components/shortlink`) replace the original's
  Firebase Dynamic Links dependency with a self-hosted table and a Go
  QR-code library: `POST /v1/shortlinks` (authed) mints a short link for
  any target URL; `GET /s/:code` (public) redirects and records a click;
  `GET /s/:code/qr` (public) serves its QR code as a PNG. Set
  `SHORTLINK_BASE_URL` to the domain a generated QR code should point at.

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
