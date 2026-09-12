# wallet-backend — Full Base Blockchain Port of the Trovo Wallet API

> **Revision note (v3):** v1 targeted Stellar by mistake. v2 corrected the
> target chain to Base but scoped the port as a trimmed "base template,"
> deliberately dropping tokenization, patron memberships, and the
> servicelinks partner API as "business-specific." That scoping was wrong:
> the actual ask is a **full port of everything** in
> `trovo-wallet-monorepo/backend` to Base, improvising only where a Stellar
> primitive has no direct EVM equivalent. This revision replaces the old
> "what gets dropped entirely" section with a complete, subsystem-by-subsystem
> mapping instead, built from an exhaustive audit of the original codebase
> (every route, every model, every background worker — see §4 for the
> full inventory this plan is checked against).
>
> **Current implementation status**: §4.1 (core wallet: auth, users,
> assets, payments, swaps, announcements, root, callbacks) and §4.2
> (shared/multi-party wallet access) are built and pushed. Everything else
> in §4 is planned but not yet implemented — this document is the checklist
> and design for that remaining work, sequenced
> in §10.

## Purpose

A full port of the Trovo Wallet API to **Base** (Coinbase's OP-Stack
Ethereum L2), preserving every feature of the original — tokenization,
patron memberships, the partner API, multi-vendor KYC and fiat rails,
shared/multi-party wallet access, account recovery, market making — with
each Stellar-specific mechanism replaced by a deliberate Base/EVM
equivalent rather than mechanically renamed. Where a Stellar primitive has
no EVM equivalent at all (trustlines, claimable balances, channel accounts,
native on-chain order books, multi-signature-by-protocol), §2 documents the
substitute design and the reasoning behind it, so the choice is visible and
reviewable rather than silently baked in.

Two decisions drive nearly every design choice below:

1. **Transaction generation is the core business logic, not the chain SDK.**
   The most valuable part of the original codebase isn't "it uses Stellar" —
   it's the discipline around building a correct, replay-safe, non-custodial
   transaction and handing it off for signature. That discipline is what
   gets ported; only the concrete chain calls underneath it change. Every
   component is organized around a **Build → Sign → Submit** pipeline for
   exactly this reason.
2. **The client app must be able to work offline.** Signing is the one step
   that must never require connectivity. §3 makes this a first-class API
   design constraint across every mutating endpoint, including the new
   subsystems added in this revision.

## 1. Why Base changes the design (and what stays the same)

Base is an OP-Stack Ethereum L2: standard Ethereum JSON-RPC, secp256k1/ECDSA
keys, 0x-addresses, standard EVM transactions. Concretely:

- **No bespoke chain SDK is needed at all.** `github.com/ethereum/go-ethereum`
  talks to Base's RPC endpoint like any Ethereum node — no Base-specific
  library exists or is required.
- **Assets are ERC-20 contracts, not issuer/trustline pairs**, and — unlike
  Stellar — **issuing a new asset means deploying a contract**, not just
  configuring an issuer account. This is the single biggest structural
  difference the tokenization subsystem (§4.9) has to absorb.
- **Fees are gas** (EIP-1559 `maxFeePerGas`/`maxPriorityFeePerGas`), not a
  flat per-operation fee.
- **There is no protocol-level multi-signature or native DEX/order book.**
  Stellar accounts can require M-of-N signatures natively, and Stellar has a
  built-in on-chain order book. Base has neither at the protocol level —
  both require a deliberate design substitute (§2).
- **Everything not chain-specific is unaffected**: the DI pattern,
  component-per-vertical-slice layout, error shape, DB migration harness,
  cache abstraction, and every pluggable integration interface (`notify`,
  `kyc`, `fiat`, `rates`, `alerting`, `storage`) carry over untouched — §6.

## 2. Cross-cutting primitive substitutions

Every Stellar-specific mechanism that appears in more than one subsystem,
decided once here rather than re-litigated per feature:

| Stellar primitive | Where it's used | Base/EVM substitute | Why |
|---|---|---|---|
| Per-request keypair signature auth (`publicKey+timestamp`, signed with the Stellar key, verified on every call) | The entire non-admin API | **SIWE (EIP-4361) → session JWT** — already built (`internal/components/auth`). One sign-in, then a bearer token. | Signing every single request is not how EVM wallets/dApps interact; SIWE+session is the ecosystem standard and fixes a real replay gap the original scheme had (see v2 plan for detail). Already implemented — no change needed. |
| Trustlines (`opt-in`/`opt-out`, required before holding a non-native asset) | assets, users (asset opt-in/opt-out) | **No on-chain action needed** — any address can hold any ERC-20 with zero setup. Keep the *API shape* (opt-in/opt-out endpoints, so client code barely changes) but back it with a purely off-chain "watched tokens" table: opt-in adds a row, opt-out removes it, used only to drive the wallet's UI asset list. | Preserves the original's user-facing feature (curate which tokens show in your wallet) without inventing on-chain machinery for a permission Base doesn't require. |
| Claimable balances (`claim-asset`/`reject-asset` — deliver an asset to someone who may not have a trustline yet) | users (claim/reject), tokenization delivery | **Server-held escrow per pending claim.** The sender's transfer goes to a per-claim address derived via `cryptoutil.DeriveKey(seed="claim:"+claimID)`; claiming triggers a server-executed transfer from that address to the claimant, rejecting refunds the sender. Recorded in a `PendingClaim` table mirroring `TransactionStatus` semantics. | A true on-chain escrow contract is the "more correct" answer but means writing, auditing, and deploying Solidity beyond the tokenized-asset contract this port already requires (§2's asset-issuance row); the derived-address escrow reuses a pattern already in this codebase (server-derived signer roles) and needs no new contract. Documented here as a deliberate, revisitable simplification — an `Escrow.sol` contract is the natural v2 upgrade. |
| Channel accounts (a pool of alternate signer accounts used to fee-bump/sponsor transactions without contending the primary account's sequence number) | Server-sponsored transactions during account activation and shared-access/fiat-purchase flows | **Not needed as infrastructure.** EVM has no fee-bump equivalent for an already-signed transaction (only ERC-4337 paymasters do this, explicitly deferred to v2 per §11). Where the original used channel accounts to let Trovo pay for a user's activation transaction, the direct Base equivalent — already how the original's Flutterwave-activation webhook behaves — is **funding the user's own address with a small amount of ETH** so they can pay their own gas. No pool, no sequence contention, because nonce contention on Base is inherently per-address (see next row). |
| Sequence-number contention across many server-signed transactions from one shared role | Market-making, bulk-payment, recovery signer roles | **Per-address nonce tracking**, not a pool of alternate accounts: an in-memory (or Redis-backed) nonce counter per derived signer address, incremented under a mutex, seeded from `PendingNonceAt` on first use. | This is strictly simpler than Stellar's channel-account workaround because EVM's nonce model doesn't have the "one bad transaction blocks the whole account" fragility Stellar sequence numbers have to the same degree — a tracked counter is sufficient. |
| Path-payment strict-send/receive swaps against Stellar's **built-in on-chain DEX** | swaps, tokenization pricing, account-activation pricing | **DEX router contract calls** — already built generically (`internal/components/swaps`, config-driven router/ABI/method). | Already done in v2; carries forward unchanged. |
| Stellar DEX order-book/price endpoints (`GetOrderBook`, `GetChartRecords`, `GetXBNDollarAskPrice`, `GetNativeAskPrice`) | assets (order-book summary) | **Read a DEX pool's on-chain price** (Uniswap V3 pool `slot0`/TWAP oracle) for the "ask price" endpoints; for chart/history data, either point at a subgraph/indexer (external, config-driven URL) or keep the existing `rates.Provider` static/fixture default until a real data source is chosen. | Base has no protocol-level order book to query the way Horizon does; reading a live pool's price is the direct on-chain equivalent for a spot rate, but historical chart data is fundamentally an indexing problem Stellar's Horizon solved for free and Base does not — flagged as needing an external service, not something `go-ethereum` alone provides. |
| Horizon operation streaming (used only for Redis cache invalidation on payment/trustline/offer events) | Background cache invalidation across the whole app | **Poll `eth_getLogs` over a rolling block range** for Transfer/Approval events touching cached addresses, on a short interval (e.g. every 2 Base blocks, ~4s). A WebSocket `SubscribeFilterLogs` variant is a drop-in upgrade if a WS RPC endpoint is configured. | Base doesn't have a Horizon-style unified operation stream; polling recent blocks for relevant logs is the standard EVM substitute and doesn't require a specialized indexer for this narrow cache-invalidation use case. |
| Deterministic keypair derivation from a server mnemonic+salt, one per signer role (`RecoveryAccountKeypair`, `MarketMakingSignerKeypair`, `BulkPaymentSignerKeypair`, plus tokenization's per-asset issuer keypair) | Account recovery, market making, bulk payment, tokenization issuance | **`cryptoutil.DeriveKey`** — already built (secp256k1, try-and-increment over SHA-256). Reused per role with the same seed/salt-per-role pattern as the original (e.g. `DeriveKey(env.RecoverySalt + "|" + username)`), plus a new per-asset role for tokenization issuance (`DeriveKey(env.TokenIssuerSalt + "|" + tokenizedAssetID)`). | Direct structural port of an already-correct pattern; no new design needed beyond wiring it into each new component. |
| Native multi-signature (M-of-N signers configured directly on a Stellar account; `PendingAuth`/`PendingTransactionSignature` collect co-signatures against one transaction envelope) | Shared/multi-party wallet access | **Off-chain M-of-N approval against a server-derived group key.** The "shared wallet" is a Base address whose key is derived via `cryptoutil.DeriveKey(seed="shared-access:"+groupID)` and never leaves the server. Members prove authorization by each signing an EIP-191 message describing the exact proposed action (not the transaction itself, since EVM transactions can't natively carry multiple signatures for one EOA); once the group's threshold of member-approvals is collected, the server builds, signs (with the group's derived key), and submits the real on-chain transaction. | The cryptographically "correct" EVM equivalent of native multi-sig is a smart-contract wallet (Safe), explicitly deferred to v2 (§11) as meaningfully more infrastructure (a deployed, audited multi-sig contract per group) than a v1 port should assume. This substitute preserves the original's exact API shape (propose → notify approvers → approve/reject → auto-execute at threshold) and its central security property (no single member can move funds alone) at the cost of introducing one point of key custody (the server) that Stellar's native multi-sig didn't have — documented plainly as the tradeoff, not hidden. |
| Asset issuance ("minting" — on Stellar, simply a payment *from* the issuer account, since an issuer's balance of its own asset is unlimited by definition) | Tokenization, crypto-deposit-to-mint bridge, servicelinks partner minting | **Deploy a minimal mintable/burnable ERC-20 contract per issued asset**, with mint authority held by a per-asset derived key (see row above). A crypto deposit "mint" becomes a real `mint()` call on that asset's contract (or, for bridging an *external* asset like deposited USDC, a `transfer()` from a custodial treasury instead of a mint, since you cannot mint someone else's token). | This is the one row with no way to avoid new on-chain infrastructure: Base has no "issuer account" concept, so representing a new tokenized asset requires an actual contract. §5 covers the Solidity/deployment work this implies. |

## 3. Offline-capable API design (unchanged from v2, applies to every new subsystem too)

Every mutating endpoint — including all the new ones added by this
revision (shared-access execution, tokenization minting/subscription,
patron subscription payment, etc.) — keeps the **Build → Sign → Submit**
split:

1. **`Build` returns everything needed to sign completely offline** (nonce,
   `maxFeePerGas`/`maxPriorityFeePerGas`/`gasLimit`, `chainId`, `to`,
   `value`, `data`).
2. **`Submit` is idempotent** via a client-supplied idempotency key.
3. **Nonce handling supports offline batching** via an optional explicit
   nonce on `Build`.

The one carve-out: flows where the *server* is the signer (shared-access
execution, tokenization minting, escrow claim release) skip the
Build/Sign/Submit split entirely, since there's no client key involved —
those are a single server-side "resolve params → sign with the derived key
→ submit" call, covered by `network.SignAndSubmitTx` (§5).

## 4. Subsystem-by-subsystem port plan

Checked directly against the exhaustive audit of
`trovo-wallet-monorepo/backend` (every controller, model, and background
worker read in full). Each subsystem lists: the original routes/models it
must account for, the Base design, and current status.

### 4.1 Core wallet, auth, payments, swaps, assets, announcements, root, callbacks — **DONE**

Implemented and pushed: SIWE auth + session JWT, user registration/profile,
curated ERC-20 token catalog + balance + allowance build/submit, native/ERC-20
payment build/submit with idempotency, generic DEX-router swap call builder,
announcements (public read + JWT-admin write), generic webhook stub, health
check. See the codebase itself and the v2 plan content preserved in git
history for the detailed original-vs-Base mapping table.

### 4.2 Shared/multi-party wallet access — **DONE**

Original: `PendingAuth`, `PendingTransactionSignature`, `ClosedGroup`,
`UserClosedGroup`, `WalletPermission`; routes under `/v1/shared-access/...`
(enable/modify/disable shared access, list/get/approve/reject pending
approvals, wallet-balances) plus a `/v1/shared-access/...` twin of nearly
every mutating endpoint elsewhere in the API (payment, swap, withdrawal,
asset opt-in/out, claim/reject, tokenization subscription/early-exit).

Base design: per §2's multi-sig substitution.

| Original model | Base model |
|---|---|
| `WalletPermission` | `GroupMember{GroupID, MemberAddress, Role}` (`INITIATOR`/`APPROVER`/`VIEW_ONLY`) |
| `ClosedGroup`/`UserClosedGroup` | `ClosedGroup{ID, Name, Threshold, Address}` (`Address` = the group's derived Base address) + `GroupMember` |
| `PendingAuth` | `PendingAction{ID, GroupID, ProposerAddress, Kind, To, TokenAddress, Value, Data, RequiredApprovals, Status, TxHash}` |
| `PendingTransactionSignature` | `PendingActionApproval{ID, PendingActionID, MemberAddress, Signature}` |

Routes: `/v1/shared-access/groups` (create/modify/disable),
`/v1/shared-access/actions` (propose), `/v1/shared-access/actions/:id/approve`,
`/v1/shared-access/actions/:id/reject`, `/v1/shared-access/actions`
(list pending), `/v1/shared-access/balance/:groupId`. Rather than a
`/v1/shared-access/...` twin of every other endpoint, a shared-access
payment/swap/approval is a `PendingAction` with `Kind: "payment"` or
`"swap"` whose `To`/`Data` fields are populated the same way the regular
payments/swaps `Build` step would — one generalized propose/approve/execute
flow instead of duplicating each component's route surface, which is a
simplification worth calling out (the original's near-total endpoint
duplication is what a from-scratch design would avoid).

### 4.3 Account security & recovery — **planned**

Original routes: security questions (save/list/verify), email-OTP
recovery request/verify, enable/disable account recovery, do-recovery
(replaces the account's signer, revokes any shared-access permissions the
old signer held), inactive-account recovery variant. Models:
`SecurityQuestion`/`UserSecurityAnswer` (already ported in v2),
`UserAccountRecoveryLog`, `UserAccountRecoveryEmailVerification`,
`UserMobilePhoneVerification`, `ReservedName`.

Base design: the flow is chain-agnostic except for the final step. On
Stellar, recovery replaces the account's *signer key* (the account address
itself never changes). On Base, an EOA's address *is* derived from its key
— there's no way to "re-key" an address the way Stellar accounts support.
**Recovery on Base therefore means re-pointing the `User.Address` to a new
address the user proves ownership of via SIWE**, gated by the same
security-question + email-OTP factors as before, with the recovery-signer
role (`cryptoutil.DeriveKey`-derived) co-signing an on-chain attestation of
the change rather than a Stellar `SetOptions` operation. Any shared-access
`GroupMember` rows for the old address are revoked and other group members
notified, exactly matching the original's security posture.

### 4.4 KYC — **planned**

Original: Sumsub (outbound applicant-creation/SDK-token API + HMAC-verified
webhook, levels 1-3) and Doja/Dojah (webhook-only, BVN-centric, levels 1-4,
IP-allowlisted). Models: `KYCConfig`, `KYCLevel`, `SumSubReviewResult`,
`UserKYCProgress`, `DojaWidget`, `UserDojaKYCProgress`.

Base design: **entirely chain-agnostic** — these vendor integrations don't
touch the blockchain at all. Ports as real, working integration code
against the same vendor APIs (Sumsub's actual REST endpoints, Doja's actual
webhook payload shape), slotted behind the `internal/kyc.Provider` interface
already defined in this codebase (replacing the placeholder
`ManualKYCProvider` default with `SumsubProvider`/`DojaProvider`
implementations). The legacy OneLiquidity facematch/compliance path found
in the audit is **dead code upstream** (implemented, never routed, not
migrated) — see §9 for whether to port it at all.

### 4.5 Fiat payments & activation — **planned**

Original: Flutterwave webhook-driven activation (dispenses gas + reward
token from a faucet wallet) and asset-purchase (two-phase: pre-sign the
on-chain leg, create an invoice, submit on webhook confirmation). Models:
`FiatPaymentConfig`, `FiatPaymentInvoice`, `FiatPayment`,
`PaymentWebhookRequest`, `FaucetConfig`, `ActivationAmount`, `ServiceFee`,
`ServiceLinkServiceFee`, `FeeCollection`.

Base design: the decoupled build/pay/submit pattern (§5.2 of the v2 plan,
unchanged) plus the channel-account-free activation design from §2 — the
"activation" webhook builds and submits a small ETH transfer (gas) plus an
ERC-20 transfer (reward token) from a faucet-role derived key, instead of
a Stellar payment from a hardcoded faucet secret key. `internal/fiat`
already defines the `Processor` interface this plugs into; this phase adds
a real `FlutterwaveProcessor` implementation.

### 4.6 Stablerail (NGN/cNGN rail) — **planned**

Original: BVN onboarding (triggered by Doja Level-1 KYC completion),
cNGN on-ramp (virtual account generation + polling), bank-list sync.
Models: `StablerailConfig`, `StablerailUser`, `StablerailOnboardUserRetry`,
`StablerailRequest`, `StablerailOnramp`, `StablerailOfframp`,
`StablerailBank`. **Note**: the audit found `StableRailInitiateOfframp` and
`StableRailInitiateAssetWithdrawal` implemented upstream but never wired to
a route — see §9.

Base design: chain-agnostic vendor integration, same shape as KYC/Flutterwave
— real API calls against Stablerail's actual endpoints, on-ramp completion
mints/transfers cNGN-equivalent (or bridges to a Base-native stablecoin,
most naturally USDC) to the user's Base address instead of a Stellar asset.

### 4.7 Crypto deposit/withdrawal — **planned**

Original: OneLiquidity-backed deposit-address generation, withdrawal
network listing (cached from OneLiquidity every 800s),
withdrawal-request queueing, deposit webhook → `CallbackDepositItem` →
background "minting initiator" loop that mints the equivalent on-chain
asset. Models: `CryptoWalletDepositAddress`, `CryptoDeposit`,
`CallbackDepositItem`, `WithdrawalNetwork`, `CryptoWithdrawal`,
`WithdrawalRequest`.

Base design: same OneLiquidity vendor integration (chain-agnostic — it's a
custodial deposit/withdrawal network, not Stellar-specific); the
deposit-to-mint bridge becomes deposit-to-**mint-or-transfer** per §2's
asset-issuance row (mint for Trovo-issued assets, transfer from a custodial
treasury for pass-through assets like deposited USDC).

### 4.8 Market making (trades/offers) — **planned, needs an explicit decision**

Original: `postUsersTradesHandler` places a resting limit offer directly on
Stellar's built-in DEX order book (`MarketOffer` model,
`services/market_making.go`).

Base has no protocol-level order book to place a resting offer on — this
needs one of two designs, and unlike the rest of this plan I'm not picking
one silently:

- **(a) Off-chain order book, server-matched**: the server holds proposed
  offers in a `MarketOffer` table and matches compatible buy/sell orders
  itself, executing a transfer between the two parties' addresses (via the
  shared-access-style server-signed execution, or a build/sign/submit pair
  from each side) when a match is found. Preserves the "place a resting
  limit order" UX exactly, at the cost of the server now matching trades
  rather than a public DEX doing it.
- **(b) Swap-only, no resting orders**: drop the resting-limit-offer
  concept entirely and treat "market making" as just using the existing
  swaps component against a DEX at whatever the current pool price is.
  Much simpler, but is a real feature reduction, not just an
  implementation detail.

I'm flagging this rather than guessing — see §11.

### 4.9 Tokenization — **planned, the largest subsystem**

Original: ~55 model types (`models/tokenization.go`, 6,830 lines),
7-value status state machine (Draft → Application Confirmed → Fee Confirmed
→ Fee Acknowledged → Minted → Primary Sale Active → Secondary Sale Active,
each transition on a specific route or background worker), covering:
creation/application intake, vetting, sales-date management, fee
payment+proof+acknowledgement, document/logo upload, minting (with its own
`MintingApprovers` CSV gate distinct from shared-access), primary
sale/subscription (crypto **and** the two-phase fiat flow), expression of
interest (pre-launch waitlist + notification-on-activation), secondary
sale/trading, early exit (with penalty-percentage payout), closed-group
(private offering) gating, and a large reference-data surface (sectors,
sub-sectors, types, document types, custodians, managers, issuing houses,
legal partners, rating agencies, trustees, fees, currencies, protection
options, proceed cycles, allowed countries, country configs, minting
approver/initiator allow-lists). Background workers: primary-sales
activation (every 5s), secondary-sales activation (every 5s),
post-tokenization trustline automation (every 5s), interest-notification
(every 5s). Public discovery endpoint (`/v1/public/tokenization`), full
admin surface (`/v1/trovo-manager/tokenization/...`), and partner-API
passthrough (`/v1/trovo-api/assets/...`).

Base design: this is where §2's asset-issuance substitution is load-bearing
for the whole subsystem. Concretely:

- **Minting (status → 4)** deploys a minimal mintable/burnable ERC-20
  contract for the asset (see §5) instead of Stellar's "assign an issuer
  keypair" step; `AssignIssuingWallet` becomes "derive the per-asset issuer
  key and deploy its contract."
- **Trustline automation (post-tokenization)** is dropped per §2 — nothing
  to automate, since Base holders need no opt-in step. The background
  worker that did this on Stellar (`ProcessPostTokenizationTrustline`) has
  no Base equivalent and is removed, not ported.
- **Primary sale (crypto)** is a `transferFrom`-style purchase against the
  asset contract (buyer approves the settlement token, contract call pulls
  payment and mints/transfers the tokenized asset) — the exact multi-step
  atomic pattern Stellar achieved in one transaction now needs either two
  transactions (approve, then buy) or a dedicated sale contract that holds
  both legs atomically; recommend a dedicated minimal `Sale.sol` contract
  per asset (buyer approves once, `buy()` does the pull+mint atomically) to
  preserve the original's one-step purchase UX rather than accepting the
  two-transaction approve/buy split as the default.
- **Primary sale (fiat)** keeps the exact decoupled build/pay/submit
  pattern (§2/§5 of earlier revisions) unchanged — this was never
  Stellar-specific.
- **Early exit** becomes a contract call (burn the held asset amount,
  transfer the penalty-adjusted payout) rather than a Stellar payment from
  a distribution wallet.
- **Sales-activation background workers** (primary/secondary) port
  directly — same 5s-poll shape, checking `SalesStart`/`SalesEnd` against
  `time.Now()` and flipping status, just against the Base-model
  `TokenizedAsset` row instead of the Stellar one.
- **The dormant payout-schedule engine** (`ProceedPayout`,
  `TokenizedAssetPayoutSchedule`, `TokenizedAssetPayoutEngineTask`) was
  found *incomplete and unwired* upstream (hardcoded to a placeholder
  asset, no route or worker calls it) — see §9 for whether to finish it
  properly as part of this port or explicitly leave it out.
- **Reference data** (sectors, custodians, fees, etc.) is chain-agnostic —
  ports as a direct schema translation, no design decision needed.
- **Closed groups (private offerings)** reuses the same `ClosedGroup`
  concept as shared-access (§4.2) for *membership gating* rather than
  *wallet control* — worth a shared underlying table (`ClosedGroup` +
  `ClosedGroupMember`) with a `Purpose` discriminator (`WALLET_ACCESS` vs
  `PRIVATE_OFFERING`) rather than two parallel implementations.

### 4.10 Patron/membership — **planned**

Original: `PatronPackage`/`PatronTier`/`PatronMembershipGrade` (pricing
matrix), `UserPatronMembership` (active membership),
`UserPatronSubscriptionLog` (history, supports future-dated effective
changes), `PatronSubscriptionPaymentAsset`. Subscribe via a standard
payment to a dedicated `PATRON_FEE_WALLET`.

Base design: fully chain-agnostic business logic — the only change is the
payment leg (native/ERC-20 transfer via the existing payments Build/Submit,
to a `PATRON_FEE_WALLET` config value instead of a Stellar address). **Fix
included, not just ported**: the audit found the original's activation
worker (`UpdateUserPatronMemberships`, which promotes future-dated pending
subscriptions) runs **once at boot only**, not on a recurring schedule —
a subscription that becomes effective while the process keeps running past
that point is never promoted until the next restart. The Base port makes
this a proper recurring worker (matching the cadence of the tokenization
sales-activation workers) rather than reproducing the bug.

### 4.11 Servicelinks partner API — **planned**

Original: ~30 routes across two prefixes (`/v1/servicelinks/...`,
`/v1/trovo-api/...`), API-key-authenticated, covering login delegation
(request/approve/verify → issues the partner a JWT for that user), a
generic "authorize" 2FA-style deep-link flow, an "events" registration flow,
tokenized-asset info lookup, user-directory lookup, push-notification
relay, partner-driven user onboarding, KYC-status override (scoped to
partner-created users), partner-driven minting, sub-wallet creation,
balance/payment-history lookup, and a full tokenization-management
passthrough (apply, upload logo/documents, confirm application, confirm/ack
fees, admin/marketplace listing, primary-sale execution, deletion) plus a
separate private stakeholder-document store. `ServiceLink` model carries a
capability bitset (`LoginPermission`, `PaymentPermission`,
`TokenInfoPermission`, `AuthorizationPermission`, `EventPermission`,
`PushNotificationPermission`, `TokenizedAssetAuthorizationPermission`,
`CreateUsersPermission`, plus data-sharing flags).

Base design: entirely chain-agnostic as an API-key/permission-bitset
authorization layer sitting in front of the same underlying
users/payments/tokenization services this plan already covers — no new
blockchain design needed here, just the permission-gated routing layer
itself, reusing the API-key middleware pattern from the original
(`internal/middleware`, currently only SIWE+JWT — add an `APIKeyAuth`
middleware alongside it) and each underlying capability's Base
implementation from the sections above.

### 4.12 Admin surface ("Trovo Manager" equivalent) — **planned**

Original: JWT-authenticated admin routes for tokenization vetting/minting/
fee-acknowledgement/sales-date management/deletion, wallet-balance lookup,
and (implicitly, via the models) KYC-config and country-config management.
Currently this codebase only has one example admin route
(`POST /v1/admin/announcements`).

Base design: chain-agnostic — extends the existing `middleware.JWTAuth(...,
middleware.AudienceAdmin)` pattern to a proper admin route group per
subsystem (tokenization vetting/minting, KYC config, patron config, wallet
lookup), rather than inventing a new auth mechanism.

### 4.13 Reference data & misc — **planned**

Banks (`Bank`), dynamic JSON forms (`JsonForm`), country/country-config
(`Country`/`CountryConfig` — per-country fee percentages, activation
amounts, regulator info), reserved usernames (`ReservedName`), geo-IP
lookup (`ipapi` integration feeding registration risk fields), Discord
alerting parity (`internal/alerting` already supports this — extend its use
to the same failure points the original logs: failed payments/swaps, low
faucet balance, DB pool warnings). All chain-agnostic; direct schema/logic
translation.

**Firebase Dynamic Links (deep links, QR codes, shortlinks) — improvised
replacement.** The original leans on Firebase Dynamic Links for every
generated link (referrals, login/authorize/event deep links, payment
requests). Since this codebase already avoids a hard Firebase dependency
(per the v2 plan's `notify`/`storage` design), the Base port replaces this
with a **self-hosted shortlink table** (`DynamicLink{ID, ShortCode,
TargetURL, Metadata}`) plus a Go QR-code library — same feature, one fewer
required third-party account, consistent with this codebase's existing
"free/local default, real vendor optional" pattern for every other
integration.

## 5. New infrastructure this port requires that didn't exist before

- **A minimal Solidity ERC-20 contract** (mintable, burnable, ownable),
  used once per tokenized asset (§4.9) and, if needed, for any
  Trovo-issued reward/utility token. Compiled ahead of time (not at
  runtime) and embedded as ABI+bytecode; deployed via
  `go-ethereum/accounts/abi/bind`'s `DeployContract`, with the deploying/
  mint-authority key coming from `cryptoutil.DeriveKey` per §2.
- **A minimal `Sale.sol` contract** for atomic primary-sale purchases
  (§4.9), so a buyer's approve+buy stays a predictable two-step flow
  instead of a bespoke multi-transaction dance per asset.
- **On-chain price reading** (Uniswap V3 pool `slot0`) added to
  `internal/network`, for the assets order-book/price-discovery
  replacement (§2).
- **A chain-log polling worker** replacing Horizon operation streaming
  for cache invalidation (§2).
- **An API-key auth middleware** for the servicelinks partner surface
  (§4.11), alongside the existing SIWE/JWT middleware.
- **A self-hosted shortlink + QR service** replacing Firebase Dynamic
  Links (§4.13).

## 6. What still carries over as-is (chain-agnostic)

Unchanged from v2, and now also the landing point for every new vendor
integration this revision adds:

| Area | Notes |
|---|---|
| Boot sequence & DI pattern (`main.go`, `GlobalConfig`) | Unaffected by chain choice or subsystem count. |
| Layered component structure | `controllers/services/models` per component. |
| Error shape (`internal/apperrors`) | Unaffected. |
| DB migration harness (`internal/db`) | Unaffected — every new model above still goes through the same `AutoMigrate` list. |
| Cache abstraction (`internal/cache`) | Unaffected; also backs SIWE nonces and the new shared-access/tokenization polling workers' dedup. |
| `notify`, `storage`, `kyc`, `fiat`, `rates`, `alerting` interfaces | Real implementations (Sumsub, Doja, Flutterwave, Stablerail, OneLiquidity, Discord) now plug into these existing interfaces per §4 — no interface changes needed, just new concrete types. |
| `announcements`, `root`, `callbacks` components | Unaffected. |

## 7. Target module layout (revised for full scope)

```
wallet-backend/
├── main.go
├── contracts/                      # Solidity sources + compiled ABI/bytecode (§5)
│   ├── MintableToken.sol
│   └── Sale.sol
├── internal/
│   ├── sharedconfig/  db/  cache/  apperrors/  cryptoutil/  validators/   # unchanged
│   ├── network/                     # + on-chain price reads, log polling, SignAndSubmitTx
│   ├── middleware/                   # + APIKeyAuth for servicelinks
│   ├── notify/ storage/ rates/ alerting/          # unchanged interfaces
│   ├── kyc/                          # + SumsubProvider, DojaProvider
│   ├── fiat/                         # + FlutterwaveProcessor, StablerailProcessor
│   ├── shortlink/                    # NEW: self-hosted dynamic-link + QR replacement
│   └── components/
│       ├── root/ announcements/ callbacks/                  # unchanged
│       ├── auth/ users/ assets/ payments/ swaps/             # done (§4.1)
│       ├── sharedaccess/             # NEW (§4.2)
│       ├── recovery/                 # NEW (§4.3) — or folded into users, TBD during implementation
│       ├── kyc/                      # NEW (§4.4) — HTTP layer over internal/kyc
│       ├── fiat/                     # NEW (§4.5) — HTTP layer over internal/fiat
│       ├── stablerail/               # NEW (§4.6)
│       ├── crypto/                   # NEW (§4.7) — deposit/withdrawal
│       ├── marketmaking/             # NEW (§4.8, pending the design decision)
│       ├── tokenization/             # NEW (§4.9) — the big one
│       ├── patron/                   # NEW (§4.10)
│       ├── servicelinks/             # NEW (§4.11)
│       └── admin/                    # NEW (§4.12)
└── docs/
```

## 8. Base network configuration (unchanged)

| | Chain ID | RPC (public default) |
|---|---|---|
| Base Mainnet | 8453 | `https://mainnet.base.org` |
| Base Sepolia (testnet) | 84532 | `https://sepolia.base.org` |

## 9. Explicitly dead/dormant upstream code — needs a decision, not a guess

The audit found three pieces of the original that are implemented but not
actually live in production today. Porting is not free, so each needs an
explicit call rather than silent inclusion or silent omission:

1. **The tokenization payout-schedule engine** (`ProceedPayout`,
   `TokenizedAssetPayoutSchedule`, `TokenizedAssetPayoutEngineTask`) —
   models exist, a batch-computation helper exists, but it's hardcoded to a
   placeholder asset and wired to no route or worker. Options: (a) finish
   it properly as part of this port, (b) port the models/schema only
   (so the door stays open) without wiring logic, (c) skip entirely.
2. **Legacy OneLiquidity facematch/compliance KYC**
   (`FacematchPassport`/`FacematchNationalID`/`FacematchDrivingLicense`/
   `GovermentIDProofOfResidency`, `ComplianceStartNewVerification`) —
   fully implemented, zero routes reference it, not even in the original's
   migration list. Superseded by Sumsub/Doja. Recommend: skip — porting
   genuinely dead code adds surface area for no live benefit.
3. **`StableRailInitiateOfframp` / `StableRailInitiateAssetWithdrawal`** —
   implemented service functions with no calling route upstream. Options:
   (a) add the missing routes as part of this port (finishing what looks
   like an oversight upstream), (b) port as internal-only functions with no
   route, matching upstream exactly, (c) skip.

My recommendation, to unblock without guessing on the two ambiguous ones:
skip #2 (clearly dead, superseded), and for #1 and #3 default to (b) —
port the models/functions faithfully but don't invent the missing
route/wiring that upstream itself never finished, unless told otherwise.

## 10. Phased build roadmap

Sequenced by dependency and by how much new infrastructure each phase
needs, not strictly by the order features appear above.

| Phase | Scope | Depends on | Status |
|---|---|---|---|
| 0 | Core wallet/auth/payments/swaps/assets (§4.1) | — | **DONE** |
| 1 | Shared/multi-party wallet access (§4.2) | Phase 0 | **DONE** |
| 2 | Account security & recovery (§4.3) | Phase 0 |
| 3 | KYC — Sumsub + Doja (§4.4) | Phase 0 |
| 4 | Fiat payments & activation — Flutterwave (§4.5) | Phase 0, benefits from Phase 3 (activation often gated on KYC) |
| 5 | Stablerail (§4.6) | Phase 3 (BVN/KYC-triggered onboarding) |
| 6 | Crypto deposit/withdrawal — OneLiquidity (§4.7) | §5's contract-deployment infra (for the mint side) |
| 7 | Market making (§4.8) | §11's design decision |
| 8 | On-chain infra: Solidity contracts + deployment helper, price reading, log polling (§5) | Needed before Phase 9 |
| 9 | Tokenization (§4.9) | Phase 8, Phase 1 (closed-group reuse), Phase 4 (fiat purchase flow) |
| 10 | Patron/membership (§4.10) | Phase 0 |
| 11 | Servicelinks partner API (§4.11) | Nearly everything above, since it's a passthrough layer |
| 12 | Admin surface (§4.12) | Whatever subsystems exist by then |
| 13 | Reference data, shortlinks, geo-IP, Discord alerting parity (§4.13) | Can run in parallel with any phase |
| 14 | Full integration pass: build/vet/test, smoke test against Base Sepolia, README/docs polish | Everything |

## 11. Open decisions needing input before implementation proceeds

- **Market making design (§4.8)**: off-chain server-matched order book vs.
  swap-only. This changes the feature, not just the implementation —
  needs a call, not an assumption.
- **Dead/dormant upstream code (§9)**: confirmed recommendation is skip the
  facematch KYC path, port-without-wiring the payout engine and the two
  unrouted Stablerail functions — flag if that's wrong.
- **Tokenized-asset contract standard**: plain mintable ERC-20 (matches the
  original's actual on-chain simplicity — Stellar assets aren't a
  "security token standard" either) vs. a compliance-aware standard like
  ERC-1400. Recommend starting with plain ERC-20 plus the existing
  off-chain compliance/KYC gating, since that's what the original actually
  does today.
- **CockroachDB `PaymentHistory` tracking table**: the original runs a
  second database purely for `TrackedWallet`/`TrackedPublicKey`
  ops-monitoring, migrated separately from the main CI migrator. Recommend
  folding into the main Postgres instance — it's redundant with the
  payments component's own `PaymentHistory` table and isn't worth a second
  database dependency.
- **Smart accounts (ERC-4337)** — still out of scope for this port, still
  the natural v2 (§2's multi-sig and asset-issuance rows both note where a
  smart-contract wallet or paymaster would strictly improve on the chosen
  substitute).
- **Vendor integrations you can't test live**: Sumsub, Doja, Flutterwave,
  Stablerail, and OneLiquidity all need real credentials to test end-to-end
  and none are chain-specific — recommend writing the real integration
  code against each vendor's actual documented API shape now (translatable
  directly from the original's working calls) rather than stubbing them,
  and testing what can be tested (request construction, signature/HMAC
  verification, response parsing against recorded fixtures) without live
  credentials.
