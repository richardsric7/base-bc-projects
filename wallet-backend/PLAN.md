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
> **Current implementation status**: every subsystem in §4 (§4.1 through
> §4.13) is built, tested, and pushed — the full 14-phase roadmap in §10 is
> **DONE**. This document remains the design record and rationale for every
> port decision (what changed, what was substituted, what was dropped and
> why), not a pending checklist.

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
| Per-request keypair signature auth (`publicKey+timestamp`, signed with the Stellar key, verified on every call) | The entire non-admin API | **Superseded — see §12.** SIWE+session-JWT was tried first (`internal/components/auth`) and works, but the project's own decision is to restore the original's per-request signature model instead: personal_sign (EIP-191) over secp256k1 in place of ed25519, keeping the original's exact two-header signer/wallet split. §12 covers the design, including the one real gap in the original (no timestamp-freshness check at all) that this fixes rather than reproduces. | Per-request signing suits this port's actual client (`wallet-web`'s embedded, non-extension signer, which signs silently with no user-facing wallet popup) better than a stolen-bearer-token-vulnerable session scheme, and it's a closer match to what the original app's mobile client already expected. |
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
| `ClosedGroup`/`UserClosedGroup` | `ClosedGroup{ID, Name, Purpose, Threshold, Address}` (`Address` = the group's derived Base address, nil unless `Purpose == WALLET_ACCESS`) + `GroupMember` |
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

`ClosedGroup` also carries a `Purpose` field (`WALLET_ACCESS` — the
default, everything above — or `PRIVATE_OFFERING`), added ahead of Phase
9: tokenization's private-offering gating reuses this same table and
`GroupMember` as a plain membership allow-list rather than a parallel
`ClosedGroup`-shaped table of its own, per §4.9. A `PRIVATE_OFFERING` row
has no `Address`/`Threshold` (no wallet, no approval flow) and never
appears in this section's routes.

### 4.3 Account security & recovery — **DONE**

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
address**, gated by three factors: every configured security answer, a
valid unexpired email OTP, and a `personal_sign` signature over a canonical
recovery message produced by the *new* address's own key (the same
non-custodial ownership proof `Register` enforces via SIWE, so recovery can
never attach a username to an address no one can actually sign from — see
`internal/components/users/services/recovery.go`). A recovery-authority
key (`cryptoutil.DeriveKey`-derived from `RECOVERY_AUTHORITY_SALT`) then
signs an off-chain `AccountRecoveryLog` attestation of the exact change —
username, old address, new address — as a tamper-evident record standing
in for the on-chain co-signature the original's native Stellar multi-sig
recovery used; there's nothing to co-sign on-chain since re-pointing which
address controls a username is a purely application-level change. Any
shared-access `GroupMember` rows for the old address are revoked (not
transferred) in the same transaction, matching the original's security
posture of requiring other group members to manually re-invite a recovered
account once satisfied the recovery is legitimate. `UserMobilePhoneVerification`
was dropped — no SMS provider is wired into the base template (see
`internal/notify`'s doc comment) and the two remaining factors (security
answers + email OTP + new-address signature) already exceed the original's
minimum bar; a real deployment wiring in an SMS provider can add phone OTP
as a fourth factor without changing this design. `request-otp` intentionally
always returns `204` regardless of whether the username exists or has
recovery enabled, so the endpoint can't be used to enumerate accounts —
matching the original's anti-enumeration posture. Implemented, unit-tested
(18 tests covering success, OTP expiry/replay, wrong/incomplete answers,
forged new-address signatures, invalid addresses, and reserved-username
registration), and documented in the README's "Account recovery" section.

### 4.4 KYC — **DONE**

Original: Sumsub (outbound applicant-creation/SDK-token API + HMAC-verified
webhook, levels 1-3) and Doja/Dojah (webhook-only, BVN-centric, levels 1-4,
IP-allowlisted). Models: `KYCConfig`, `KYCLevel`, `SumSubReviewResult`,
`UserKYCProgress`, `DojaWidget`, `UserDojaKYCProgress`.

Base design: **entirely chain-agnostic** — these vendor integrations don't
touch the blockchain at all, so this ported close to line-for-line rather
than being redesigned for Base. Implemented as a new
`internal/components/kyc` component (models/services/controllers, same
shape as every other component) rather than forcing both vendors behind
the pre-existing `internal/kyc.Provider` interface: that interface's
single-level start/webhook/status shape can't represent Sumsub's and
Doja's actual mechanics (ordered multi-level progress, per-vendor webhook
payloads, Doja's widget-ID-to-level lookup) without losing real business
logic, so `internal/kyc.Provider`/`ManualKYCProvider` remains available
as a separate, simpler opt-in for a project that hasn't picked a vendor,
while `GlobalConfig.KYC` stays wired to it by default.

Ported: real Sumsub REST client (create applicant, fetch applicant info,
generate SDK access token, all with the documented
X-App-Token/X-App-Access-Sig/X-App-Access-Ts request signing) gated by the
original's level-ordering rule (levels 1-3 must be completed in order);
Doja webhook processing with widget-ID → level/type lookup and per-level
Submitted/Completed progress tracking. `User.KYCVerifiedLevel` (ported from
the original's `User.KYCVerified`) is the single source of truth both
vendors raise (never lower) — future phases gating on KYC level (fiat
activation limits, tokenization purchase limits) read this one field
regardless of which vendor a user verified through.

Three deliberate deviations from the original, each because the original's
actual behavior was either a security bug or dependent on infrastructure
this port doesn't have yet:

1. **Dropped the client-callable "complete level" endpoint.** The original
   exposed `POST /v1/users/kyc/sumsub/complete/:levelName`, which let any
   authenticated caller mark their own KYC level done directly — no server
   verification at all, a straightforward self-approval bypass. This port
   only advances `KYCVerifiedLevel`/progress from the vendor's own
   signature-verified webhook.
2. **Fixed both webhooks' signature verification.** The original computed
   an HMAC digest for both Sumsub's `x-payload-digest` and Doja's
   `x-dojah-signature` headers but never actually compared it against the
   incoming value before trusting the payload — Doja's handler additionally
   checked the caller's source IP against one hardcoded address, but its
   failure branch had no `return`, so an unrecognized IP was only logged,
   never rejected. Net effect: neither webhook endpoint verified its caller
   at all. This port requires a valid signature on both and rejects the
   request otherwise (`Service.VerifySumsubWebhookSignature`,
   `Service.VerifyDojaWebhookSignature`).
3. **No seeded Dojah widget IDs, no push notifications, no Stablerail
   trigger.** Dojah widget IDs are specific to whichever Dojah dashboard
   account a deployer owns, so shipping the original's actual widget IDs in
   a base template would be meaningless to anyone else (and a minor
   information leak of the upstream account's config) — an operator
   configures their own via the `DojaWidget` table. Push notifications on
   KYC progress changes are commented hook points rather than implemented,
   since no device-token subsystem exists yet in this port (see §4.13);
   likewise the original's BVN-triggered Stablerail onboarding call on
   Doja level-1 completion is a hook point for Phase 5, not yet built.

The legacy OneLiquidity facematch/compliance path found in the audit
remains **dead code upstream** (implemented, never routed, not migrated) —
per §9, skipped rather than ported.

Verification: `go build`/`vet`/`gofmt` clean; 12 unit tests in
`internal/components/kyc/services` covering level-ordering enforcement, a
mocked Sumsub REST round-trip (applicant creation → SDK token), both
webhooks' signature verification (valid/forged/tampered/wrong-algorithm),
GREEN/RED Sumsub outcomes, and Doja's Pending/Completed/Failed transitions
including the never-lower-KYCVerifiedLevel rule. Real HMAC signing logic is
exercised directly (both this port's and the original's use the same
scheme), but an actual Sumsub/Doja account's credentials were not
available in this sandbox, so the live vendor API calls themselves are
unverified beyond matching their documented contract.

### 4.5 Fiat payments & activation — **DONE**

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
a real `FlutterwaveProcessor` implementation (`internal/fiat/flutterwave`).

Implemented as a new `internal/components/fiat` component. The faucet key
is `cryptoutil.DeriveKey`-derived from `FAUCET_KEY_SALT` (same pattern as
the shared-access group keys and the recovery-authority key) rather than a
live Stellar secret key stored in a `FaucetConfig` database row — an
operator funds this one derived address with ETH and the reward token
ahead of time, the same operational step as before, just with no private
key at rest anywhere. `User.Activated` (from the original's on-chain
existence check) is the idempotency guard against a duplicate webhook
delivery ever double-dispensing. The fiat-to-token conversion uses the
already-existing `rates.Provider` (currency/ETH and currency/reward-token
rates) in place of the original's live DEX order-book lookup — the same
role, a simpler and already-established abstraction; if no rate is
configured for the reward token, activation still succeeds with gas only,
degrading gracefully rather than blocking on an operator's missing config.

Two scope trims, each because a dependency doesn't exist in this port yet
(both documented as extension points, not silent drops):

1. **A single global `ActivationConfig`, not the original's per-country
   matrix.** The original priced activation per user country
   (`CountryConfig.FiatActivationAmount`/`TrovTokenActivationPercent`,
   defaulting to a hardcoded "NG" row); this port has no country/geo-IP
   subsystem yet (that's Phase 13's reference-data work) and never captured
   a user's country anywhere. `ActivationConfig` is a single seeded row an
   operator edits directly; adding a per-country lookup later is a column
   and a query change, not a redesign.
2. **The asset-purchase leg is a generic, payment-type-agnostic
   `SettlePendingInvoice`, not a Tokenization-specific handler.** The
   original's "ASSET PURCHASE" webhook branch looked up a
   `TokenizedAssetSubscription` row that doesn't exist until Phase 9.
   `SettlePendingInvoice` submits whatever pre-signed transaction a
   PENDING `FiatPaymentInvoice` carries and marks it COMPLETED, regardless
   of what the payment is for — Phase 9 will create its invoices through
   the same `CreateInvoice`/webhook path already built here, unchanged.

Also fixed while porting: the original compared its Flutterwave webhook's
`verif-hash` header with Go's plain `==` operator against the configured
secret - a non-constant-time comparison of a secret value against
attacker-controlled input. `flutterwave.VerifySignature` uses
`crypto/subtle.ConstantTimeCompare` instead.

Verification: `go build`/`vet`/`gofmt` clean; 13 unit tests in
`internal/components/fiat/services` covering the activation quote,
gas-only and gas+reward dispensing, the never-double-dispense idempotency
guard, graceful reward-token skip without a configured rate, a hard
failure without a gas rate, invoice-reference collision handling, and the
generic pending-invoice settlement path (success, missing invoice, and
no-signed-transaction, all as soft no-ops where the original was
similarly tolerant of a duplicate/late webhook delivery). No real
Flutterwave account was available in this sandbox, so
`Processor.InitiateCharge`/`VerifyWebhook` are verified against
Flutterwave's documented contract rather than a live call - and per this
port's actual flow, neither is on the path activation and asset-purchase
webhooks take (the client charges directly via Flutterwave's own SDK;
this backend only records the invoice and reacts to the webhook), so
their real-world exercise is deferred to whichever phase first needs a
server-initiated charge.

### 4.6 Stablerail (NGN/cNGN rail) — **DONE**

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

Implemented as `internal/components/stablerail`. This subsystem turned out
to be **the closest thing to chain-agnostic in this whole port**: every
Stablerail API call already takes a destination wallet address and a
"network" code as ordinary request fields, and the actual on-chain
transfer of converted funds happens on *Stablerail's own infrastructure*,
not this backend's - the withdrawal step just tells Stablerail which
address to send to. So porting to Base is one constant
(`network = "base"` in place of the original's Stellar-specific `"xbn"`)
plus using a 0x-address instead of a Stellar public key; no
`network.Client`/on-chain code exists anywhere in this component.

Ported: onboarding (`/onboarduser` + `/onboardstatus`), NGN on-ramp
(`/cngnonramp` + `/getvirtualaccount` + `/cngnonrampstatus`), the automatic
withdrawal a funded on-ramp triggers (`/withdrawasset`), and bank-list sync
(`/getbankscode`) - all real HTTP calls against Stablerail's documented
contract, with two background-goroutine pollers in `main.go` (onboarding
+ on-ramp status every 10s, bank sync every 10 minutes) mirroring the
original's polling loops exactly. The Doja Level-1-BVN-completion trigger
is wired via `kyc.Service.OnBVNVerified`, a function field main.go sets to
`stablerailServices.InitiateOnboardingByUsername` - a plain callback
rather than kyc importing stablerail directly, since these are two
otherwise-independent vendor integrations with no other reason to know
about each other.

Per §9's policy on code the audit found dead upstream: `StablerailOfframp`
(cNGN-to-bank payout) and its initiate/status functions are **not
ported** - `StableRailInitiateOfframp` had no route calling it in the
original and, unlike asset-withdrawal, no internal caller either, so
there is no working behavior to preserve, only an unfinished stub.
`StableRailInitiateAssetWithdrawal` *is* ported (as
`initiateAssetWithdrawal`), since the original genuinely used it - called
automatically once an on-ramp is funded, never as a standalone route
either upstream or here. Also dropped, both because their absence is
purely operational rather than functional: `StablerailOnboardUserRetry`
(a queue for retrying a failed onboarding trigger - the original's own
retry-queue writer is itself dead code, see the Doja webhook handler) and
push notifications on state changes (no device-token subsystem exists yet
- see §4.13), replaced with log lines at the same points.

Config: `StablerailConfig`'s `APIKey`/`BaseURL`/`FintechID` became env vars
(`STABLERAIL_API_KEY`/`STABLERAIL_BASE_URL`) rather than a database row,
consistent with every other vendor credential in this port;
`STABLERAIL_ENABLED` replaces the original's `EnableStablerail` int flag.
`FintechID` itself is dropped - grepping the original found it read
nowhere, only ever set.

Verification: `go build`/`vet`/`gofmt` clean; 9 unit tests in
`internal/components/stablerail/services` against a mocked Stablerail
HTTP server, covering onboarding (success, disabled, already-registered
skip, status-poll completion), on-ramp (requires onboarding first,
success, status-poll-to-funded triggering the withdrawal call with
`network: "base"`), and bank sync/list. No real Stablerail account was
available in this sandbox, so these calls are verified against
Stablerail's documented contract via a mocked server rather than a live
call.

### 4.7 Crypto deposit/withdrawal — **DONE**

Original: OneLiquidity-backed deposit-address generation, withdrawal
network listing (cached from OneLiquidity every 800s),
withdrawal-request queueing, deposit webhook → `CallbackDepositItem` →
background "minting initiator" loop that mints the equivalent on-chain
asset. Models: `CryptoWalletDepositAddress`, `CryptoDeposit`,
`CallbackDepositItem`, `WithdrawalNetwork`, `CryptoWithdrawal`,
`WithdrawalRequest`. The original's minting loop always mints, for every
currency, from a Trovo-issuer key — every curated Stellar asset in the
original was Trovo-issued, including wrapped BTC/ETH/USDT representations,
so there was no actual "pass-through, don't mint" path to port as-is.

Implemented as `internal/components/crypto` (chain-agnostic vendor
integration, same shape as kyc/fiat/stablerail — OneLiquidity is a
custodial deposit/withdrawal network, not Stellar-specific). Rather than
port the original's always-mint behavior unchanged, deposit crediting here
is a **transfer** from a derived treasury key holding a balance of the
matching `CuratedToken` — the deliberate Base-native choice PLAN.md
originally flagged as "mint-or-transfer" per §2's asset-issuance row.
Base's curated assets are ordinary already-deployed ERC-20 contracts this
project doesn't control minting for (unlike Stellar, where Trovo was
always the issuer by construction), so genuine on-demand minting needs a
Trovo-deployed, mint-controlled contract — Phase 9's tokenization
infrastructure, not yet built. An operator funds the treasury address
(`cryptoutil.DeriveKey`-derived from `CRYPTO_TREASURY_KEY_SALT`, same
pattern as the activation faucet and shared-access group keys) with each
curated token ahead of time; a currency with no matching `CuratedToken` is
recorded but left uncredited (logged) rather than failing the poll,
matching this port's established graceful-degradation posture.

One structural addition beyond a mechanical port: **withdrawal now
requires proof of an on-chain debit before OneLiquidity is asked to pay
out externally.** The original's "withdrawal" debited an internal Trovo
ledger balance the platform already controlled, submitted server-side as
part of the same request; on Base, a user's balance is a real on-chain
balance only they can move. `RequestWithdrawal` therefore takes the
caller's own signed transfer (to the treasury address `GetWithdrawalNetworks`
returns) and submits it via the same generic signed-tx-submission
primitive fiat's `SettlePendingInvoice` uses, before calling
OneLiquidity's withdrawal endpoint — the same build/sign/submit split
every other mutating operation in this port already follows.

**Documented gap, not a silent drop:** only single-owner withdrawal is
implemented. The original's multi-party shared-access withdrawal path
(`postSharedAccessCryptoWithdrawalsHandler`, approve-then-submit against a
group wallet's `PendingAuth` record) is not ported here — reconciling it
with `sharedaccess.PendingAction`'s executor, which today only knows how
to submit an on-chain transaction and has no notion of "then call an
external vendor API," is a real design decision, not a mechanical port. A
shared-access group's members can still withdraw external crypto once
that generalization is made; nothing about this phase's design forecloses
it. Per §9's policy on code the audit found dead upstream, the facematch/
compliance functions in the original's `oneliquidity.go`
(`ComplianceStartNewVerification`, `StartFacematchForX`,
`StartGovernmentIDCheckForProofOfResidency`) are confirmed to have no
routes calling them and are **not ported**.

Verification: `go build`/`vet`/`gofmt` clean; 9 unit tests in
`internal/components/crypto/services` against a mocked OneLiquidity HTTP
server, covering deposit-address create/reuse, deposit crediting via
treasury transfer, the graceful skip when no curated token matches a
deposited currency, deposit deduplication, withdrawal-network caching,
and withdrawal validation (min/max/unsupported-network rejection) plus a
successful withdrawal's fee math and on-chain debit submission. No real
OneLiquidity account was available in this sandbox, so these calls are
verified against OneLiquidity's documented contract via a mocked server
rather than a live call.

### 4.8 Market making (trades/offers) — **DONE**

Original: `postUsersTradesHandler` places a resting limit offer directly on
Stellar's built-in DEX order book (`MarketOffer` model,
`services/market_making.go`).

Base has no protocol-level order book to place a resting offer on — this
needed one of two designs, and unlike the rest of this plan that one
wasn't picked silently:

- **(a) Off-chain order book, server-matched**: the server holds proposed
  offers in a `MarketOffer` table and matches compatible buy/sell orders
  itself, executing a transfer between the two parties' addresses when a
  match is found. Preserves the "place a resting limit order" UX exactly,
  at the cost of the server now matching trades rather than a public DEX
  doing it.
- **(b) Swap-only, no resting orders**: drop the resting-limit-offer
  concept entirely and treat "market making" as just using the existing
  swaps component against a DEX at whatever the current pool price is.

**Chosen: (a), off-chain order book, server-matched** (user decision).
Implemented as `internal/components/market`.

The one thing (a)'s original framing didn't spell out: since Base wallets
are non-custodial, the server matching two offers still can't move either
side's asset without a signature the server doesn't have. The
implementation resolves this the same way the 0x Protocol popularized for
off-chain order books: a maker `approve()`s a derived escrow address
(`Service.EscrowAddress()`, `cryptoutil.DeriveKey`-derived from
`MARKET_ESCROW_KEY_SALT` — same pattern as every other server-key role in
this port) for the asset they're offering, and settlement pulls each
matched side's asset via `transferFrom` against that allowance rather than
ever holding funds itself between the two legs. This is a **new
mechanism**, not something to translate from the original — there, the
actual matching and settlement happened entirely inside Stellar's
protocol-level DEX once a `ManageBuyOffer`/`ManageSellOffer` operation was
submitted; the Trovo backend only recorded a local `MarketOffer` row for
UI/tracking, it never executed a trade itself.

Matching is price-time priority: a new offer matches the best-priced,
oldest-at-that-price compatible resting offer repeatedly until it's fully
filled or no more crossing offers remain, executing at the **resting**
(older) offer's price — the standard maker-price convention. Only curated
ERC-20 pairs can be traded (native ETH has no `approve`/`transferFrom`
concept, so it's rejected at `PlaceOffer` time with a clear error — a
natural extension once an escrow *contract* that can receive ETH directly
exists, Phase 8). A known, explicitly documented limitation: settling a
match is two independent `transferFrom` calls, not one atomic operation,
so if the second leg fails after the first already cleared (e.g. the
counterparty's allowance or balance changed between the two calls), the
failing side's offer is canceled but the succeeding side isn't
automatically made whole — the honest cost of not having an atomic
on-chain escrow contract yet (Phase 8's natural fix, alongside the ERC-4337
smart-account note already in §11).

Dropped rather than ported: the original's market-making fee
(`FeeChargedOnAsset`/`FeeValue`/`NetQuantity`) — its own live code had
already zeroed it out (`serviceFee.Div(decimal.NewFromInt(0))`, guarded by
`MARKET_MAKING_FEE_ENABLED` defaulting off, with the code comment "fees r
now removed"), so there was no working fee behavior left to preserve.

Verification: `go build`/`vet`/`gofmt` clean; 12 unit tests (including
table-driven input validation) in `internal/components/market/services`
covering full and partial fills, execution at the resting offer's price
even when the taker would have accepted a worse one, price-time priority
across multiple resting offers, the failed-settlement-cancels-the-
failing-side behavior, offer cancellation (ownership and status-conflict
checks), and order-book/history listing.

### 4.9 Tokenization — the largest subsystem — **DONE**

Original (per a full read of `models/tokenization.go`, 6,830 lines, and
`services/tokenized_assets.go`, 5,003 lines): a bare-`int` 7-value status
state machine (Draft(0) → Application Confirmed(1) → Fee Confirmed(2) →
Fee Acknowledged(3) → Minted(4) → Primary Sale Active(5) → Secondary Sale
Active(6)), each transition on a specific route or background worker,
covering: creation/application intake, vetting, sales-date management, fee
payment+proof+acknowledgement, document/logo upload, minting (gated by a
global staff allow-list *and* a per-asset ≥4-approver CSV multisig,
distinct from shared-access's wallet-multisig mechanism), primary
sale/subscription (crypto via Stellar path-payment DEX routing, **and** a
two-phase fiat flow deferring on-chain submission to a webhook), expression
of interest (pre-launch waitlist + notification-on-activation), secondary
sale/trading (native Stellar DEX offers against a market-making wallet),
early exit (NAV-based penalty payout, settled off-chain), closed-group
(private offering) gating, a dormant/unwired payout-schedule engine, and a
large reference-data surface (sectors, sub-sectors, types, document types,
custodians, managers, issuing houses, legal partners, rating agencies,
trustees, fees, currencies, protection options, proceed cycles, allowed
countries, country configs, minting approver/initiator allow-lists).
Background workers: primary-sales activation, secondary-sales activation,
post-tokenization trustline automation, interest-notification. Public
discovery endpoint, full admin surface (`/v1/trovo-manager/tokenization/...`),
and partner-API passthrough (`/v1/trovo-api/assets/...`).

Base design — this is where §2's asset-issuance substitution is
load-bearing for the whole subsystem, and where three already-built pieces
of this port (Phase 7's market component, Phase 8's contracts, `internal/
fiat`'s decoupled invoice pattern) do most of the work rather than needing
new machinery:

**Status model.** Same seven states, as a typed `Status string` enum
(`DRAFT`, `APPLICATION_CONFIRMED`, `FEE_CONFIRMED`, `FEE_ACKNOWLEDGED`,
`MINTED`, `PRIMARY_SALE_ACTIVE`, `SECONDARY_SALE_ACTIVE`) rather than a
bare `int` — every transition and its trigger ports 1:1 (admin
vetting sets a separate `VettingStatus` flag without moving
`AssetTokenizationStatus`, exactly as upstream).

**Core `TokenizedAsset` fields.** The identity/fee/sales-mechanics/
compliance-flag/project-narrative fields, and the ~300-field asset-class
descriptive tail (bond/mutual-fund/REIT/commodity/warehouse-receipt/vault/
private-equity-fund narrative columns), are chain-agnostic business data
with no Stellar-specific content — ported as a direct schema translation,
grouped into embedded structs by asset class for readability rather than
upstream's one flat 550-field table, with no fields dropped. What changes
is only the handful of fields that named a Stellar primitive:

| Original field(s) | Base field(s) | Why |
|---|---|---|
| `IssuingWalletPublicKey` (a Stellar issuer account) | `IssuerContractAddress` (the deployed `TokenizedAsset.sol` address) | Base's issuer is a contract, not an account — §2. |
| `MarketMakingWallet`/`WalletToHoldAssetsNotForSale` | `DistributionAddress` (a `cryptoutil.DeriveKey`-derived per-asset key holding un-sold supply and signing fiat-purchase transfers) | Same per-asset-derived-key pattern used everywhere else in this port. |
| (no equivalent — Stellar's `ManageSellOffer` *was* the sale) | `SaleContractAddress` (the deployed `Sale.sol` address) | The atomic buy() this port already built in Phase 8. |
| `AssetQuoteCurrency` + resolving a Stellar issuer via `CountryConfig.InternalBalanceTokenCode/InternalTokenIssuer` | `AssetQuoteCurrency` (a `CuratedToken` symbol, resolved directly — no separate issuer lookup) | See "Quote currency" below. |
| `ClosedGroupID` → a tokenization-only `ClosedGroup` | `ClosedGroupID` → `sharedaccess.ClosedGroup{Purpose: PRIVATE_OFFERING}` (§4.2, already built) | One table for both purposes rather than a parallel one. |
| `PostTokenizationTrustlineCandidate` + its worker | dropped entirely | No trustline/opt-in concept on Base — §2. |

**Quote currency — simplified, not just substituted.** Upstream resolves a
purchase's settlement currency through a `CountryConfig`-scoped "internal
balance token" (a synthetic intermediate Stellar asset, 1:1-minted per
purchase, existing only to let Stellar's path-payment engine route
`CNGN → NGN → AssetCode` in one atomic transaction — see
`TOKENIZATION_PLAN.md` upstream) plus a "trustline authorization required"
flag check with no ERC-20 equivalent. None of this exists to solve a
problem Base has: an ERC-20 holder needs no opt-in, so there is nothing to
authorize, and a purchase settles in exactly the token the asset is quoted
in — no intermediate currency, no multi-hop pathfinding. `AssetQuoteCurrency`
is simply a `CuratedToken` symbol (e.g. `"USDC"`); a buyer who holds a
different token swaps into it beforehand via the already-generic
`internal/components/swaps` component rather than the purchase transaction
doing an implicit conversion. `CountryConfig` keeps only the fee/compliance
fields that are genuinely per-country (SEC fee rates, VAT, application fee,
minimum-balance-to-apply); `InternalBalanceTokenCode`/`InternalTokenIssuer`
and `checkDistributionWalletHasQuoteCurrencyAuthorization` are dropped, not
ported — documented here as a deliberate simplification, not an oversight.

**Minting.** Two independent gates port directly: a global
`MintingApprover`/`MintingInitiator` staff allow-list (who may call the
mint endpoint at all), and a per-asset `MintingApprovers` CSV requiring
≥4 entries before a mint can execute. The CSV's approval collection
becomes a dedicated `MintApproval`/`MintApprovalSignoff` pair (the same
signature-over-a-canonical-description primitive shared-access's
`PendingAction`/`PendingActionApproval` uses, kept as its own table rather
than reusing that one — this is a staff sign-off on an admin action, not a
group wallet's own transaction, per the original's own separation between
the two mechanisms). Once `len(approvers)-2` signoffs are collected
(upstream's exact threshold), the server: derives a per-asset issuer key
(`cryptoutil.DeriveKey`, owns the contract) and a per-asset distribution
key (holds un-sold supply); deploys `TokenizedAsset.sol`; mints
`NumberOfTokenToBeIssued - MaxNumberOfTokenAvailableForSale` to the
distribution address and `MaxNumberOfTokenAvailableForSale` directly to a
newly-deployed `Sale.sol` (quote currency, `PricePerToken`, proceeds to the
distribution address); mints `FeeInAsset` to the fee wallet if set. No
`ChangeTrust`/`SetTrustLineFlags` steps exist to port. Status → `MINTED`.

**Primary sale (crypto).** `Sale.sol` (Phase 8) *is* the one-step atomic
purchase the original achieved via `PathPaymentStrictSend` against its own
`ManageSellOffer` — buyer `approve()`s the quote token, then `buy()` pulls
payment and releases the asset in one transaction. The backend's role
shrinks to: build the unsigned `buy()` call (the same build/sign/submit
shape every other on-chain action in this codebase uses), and record a
`TokenizedAssetSubscription` once the buyer's self-submitted transaction
confirms. No swap/pathfinding logic is needed in this component at all.

**Primary sale (fiat).** Reuses `internal/fiat`'s decoupled invoice pattern
*unchanged* — its own doc comment already names this exact upstream flow as
what it was modeled on. Since settlement here is a server-derived
distribution-key transfer rather than something the buyer must co-sign (no
trustline step exists to require the buyer's signature for), the server
signs the transfer at invoice-creation time, hands the raw signed
transaction to `fiatSvc.CreateInvoice(..., PaymentType: "TOKENIZED_ASSET_
PURCHASE", signedTransaction: &raw)`, and the *already-built, unmodified*
Flutterwave webhook handler's generic `default` branch calls
`SettlePendingInvoice` to submit it once payment clears. This drops the
original's buyer-signature round trip entirely (a real simplification, not
a feature loss — Base needs no such step) and requires zero changes to
`internal/components/fiat` itself, only a `TokenizedAssetSubscription` row
created alongside the invoice for tokenization-specific bookkeeping.

**Secondary sale/trading.** No bespoke code: Phase 7's off-chain
order-book market-making component already trades any curated ERC-20 pair.
Once an asset is minted its contract is added to the `CuratedToken`
catalog (so it's visible and, per Phase 7, immediately tradeable); reaching
`SECONDARY_SALE_ACTIVE` calls `Sale.setPaused(true)` (already built in
Phase 8) so the primary-sale contract stops accepting purchases and the
market component becomes the asset's only live trading venue — the direct
functional equivalent of upstream's "primary window closes, DEX offer
remains" without a second component to build.

**Early exit.** `TokenizedAsset.sol` is `ERC20Burnable`, so a holder can
burn their own balance directly — no server-signed "payment back to the
distribution wallet" transaction is needed at all (a simplification over
upstream, which needed the distribution-wallet hop because Stellar has no
holder-initiated burn). The backend builds the unsigned `burn(quantity)`
call, the holder self-submits it, and the server records a
`TokenizedAssetEarlyExit` row with the same NAV-based penalty-percentage
payout formula (`payoutPricePerToken = (CurrentNAVPerToken or
PricePerToken) * (1 - (EarlyExitPenalty% + EarlyExitFee%))`) for
off-chain/manual settlement — upstream itself never automated this payout
on-chain either, so this is a direct port, not a new gap.

**Expression of interest.** Direct port: a waitlist row gated on
`Status == MINTED` (upstream's literal, if slightly non-obvious, gate —
preserved as-is rather than "fixed," since it's the actual documented
behavior, not a bug). Upstream's channel-plus-separate-goroutine hand-off
to a dedicated notification worker is simplified to a synchronous
notify-all-subscribers call inside the primary-sales-activation sweep
itself — same notifications fire, one fewer moving part.

**Background workers.** Primary- and secondary-sales activation port
directly (sweep on `SalesStart`/`SalesEnd` vs. `time.Now()`), with one
fix rather than a faithful reproduction: upstream's own activation
routines block for 15 minutes *inside* a function an outer loop already
re-polls every 5 seconds — almost certainly an unintended stacking of two
different intended cadences, not a deliberate design, so this port uses a
single clean poll interval instead of reproducing the stall (the same
"fix, don't reproduce, and say so" posture already applied to the
Patron/membership activation worker, §10). Post-tokenization-trustline
automation is dropped, per §2 (nothing to automate). Interest-notification
is folded into the primary-sales sweep, per above.

**The dormant payout-schedule engine** (`ProceedPayout`,
`TokenizedAssetPayoutSchedule`, `TokenizedAssetPayoutEngineTask`) is
ported as models only, never wired to a route or worker — confirmed via
`payouts.go` (a 160-line file whose actual logic sits in a dead `func
main()` inside `package users`, hardcoded placeholder asset/issuer
constants, never called from anywhere else in the codebase) and
`TOKENIZATION_PLAN.md`'s own note that the production payout engine was
never tracked down. Per §9's resolved policy: port-without-wiring, not
skip and not finish — carrying forward the exact same "exists in schema,
does nothing" state it has upstream, documented plainly rather than
silently.

**Reference data** (sectors, custodians, managers, issuing houses, legal
partners, rating agencies, trustees, fees, protection options, proceed
cycles, allowed countries, minting approver/initiator allow-lists) is
chain-agnostic — ports as a direct schema translation. `TokenizationCurrency`
becomes a thin allow-list of `CuratedToken` symbols (no separate
issuer/contract bookkeeping — `CuratedToken` already has it).

**Closed groups (private offerings)** reuse `sharedaccess.ClosedGroup`
with `Purpose: PRIVATE_OFFERING` (§4.2, already built) rather than a
parallel table.

**Routes.** `/v1/tokenization/...` (public discovery + authed
application/subscription/early-exit/expression-of-interest, mirroring
upstream's route shape) and `/v1/admin/tokenization/...` (vetting,
fee-acknowledgement, minting, sales-date management — gated by
`middleware.JWTAuth(gc.JWTSecret, middleware.AudienceAdmin)`, the same
admin-JWT mechanism the announcements component already established, so
this doesn't need to wait on Phase 12's admin-surface work). The
`/v1/trovo-api/assets/...` partner-API passthrough is **deferred to Phase
11** alongside its API-key middleware (§5, §10) — it has no caller (the
`servicelinks` component doesn't exist yet) and every one of its routes is
a thin passthrough to a service function this phase already builds, so
nothing about tokenization itself is blocked by deferring it.

Implementation notes (what actually shipped, beyond the design above):

- `internal/components/tokenization/models`: the core `TokenizedAsset`
  table plus its ~300-field asset-class descriptive tail, grouped into
  `gorm:"embedded"` structs by asset class (bond, fund, commercial paper,
  commodity, vault, generic issuer/debt, REIT, private equity) rather than
  upstream's one flat table, purely for readability — the underlying
  schema is still one table. `EarlyExitPenaltyPercent`/
  `EarlyExitFeePercent`/`CurrentNAVPerToken`/`MaturityDate` were pulled up
  to core fields even though upstream declares them amid its REIT/
  yield-fund/bond sections, since early exit reads them unconditionally
  regardless of asset class. `MintingInitiators`/`MintingApprovers` are
  CSVs of **addresses**, not usernames as upstream has them — a signature
  is verified against an address directly, so there's no extra
  username-to-address resolution step, consistent with sharedaccess's
  `GroupMember.MemberAddress`.
- Minting's per-asset ≥4-approver signoff is its own `MintApproval`/
  `MintApprovalSignoff` pair (an EIP-191 `personal_sign` over a canonical
  message, the same primitive shared-access's `PendingAction`/
  `PendingActionApproval` uses) rather than reusing that table directly —
  a staff sign-off on an admin action is a different concept from a group
  wallet's own transaction, matching upstream's own separation between the
  two mechanisms.
- `network.Client` gained one new primitive for this phase:
  `SignTx` (sign a transaction with a server-held key and return its raw
  hex **without** submitting it), the missing piece needed to slot
  tokenization's fiat purchase settlement into `internal/fiat`'s existing
  decoupled invoice pattern (`SubmitSignedTransaction` submits it later,
  once the fiat charge clears).
- `internal/components/fiat/controllers.Init` now returns its `*services.
  Service` (previously unexported from `main.go`) so tokenization's
  `CreateFiatInvoice` hook — a function field, populated post-construction
  in `main.go`, the same pattern KYC's `OnBVNVerified` hook uses to reach
  Stablerail — can call `CreateInvoice` without tokenization importing
  `fiat/services` directly.
- One real bug caught by the test suite while porting `VetTokenizationAssetInfo`'s
  status guard: comparing `Status` (a string enum) with Go's `>` operator
  compares lexicographically, not by pipeline position (`"DRAFT" >
  "APPLICATION_CONFIRMED"` is true as strings) — fixed with an explicit
  `statusRank` lookup table rather than ever comparing `Status` values
  with `<`/`>` directly.
- Verification: `go build`/`vet`/`gofmt` clean; 20 unit tests in
  `internal/components/tokenization/services` covering the application
  lifecycle (KYC/currency/token-limit validation, the server-controlled-
  field reset security property, draft-only resubmission, vetting's
  stakeholder validation), the full mint flow end-to-end against a fake
  blockchain client (threshold-gated signoff → two contract deployments →
  `CuratedToken` listing → `Status` transition), crypto and fiat purchase
  gating (sale status, private-offering membership, the fiat-invoice hook
  wiring), and the early-exit penalty-payout formula.

### 4.10 Patron/membership — **DONE**

Original: `PatronPackage`/`PatronTier`/`PatronMembershipGrade` (pricing
matrix), `UserPatronMembership` (active membership),
`UserPatronSubscriptionLog` (history, supports future-dated effective
changes), `PatronSubscriptionPaymentAsset`. Subscribe via a standard
payment to a dedicated `PATRON_FEE_WALLET`, with the upgrade/downgrade
qualification rules and effective-date scheduling (instant vs. deferred to
the day after an existing lower-tier membership expires) hardcoded around
three fixed packages (GOLD/PLATINUM/DIAMOND) and three fixed tiers
(MONTHLY/ANNUAL/LIFETIME).

Base design: fully chain-agnostic business logic, ported field-for-field —
`internal/components/patron`. The only real change is the payment leg: a
plain native/ERC-20 transfer (via `network.Client.BuildNativeTransferTx`/
`BuildERC20TransferTx`) to a `cryptoutil.DeriveKey`-derived fee wallet
(`PATRON_FEE_WALLET_SALT`, matching every other derived fee-collection
address in this port) rather than a configured raw Stellar address, and no
DEX-routing-through-TROV to convert an arbitrary payment asset (Base has
no protocol DEX to route through — the buyer pays directly in one of a
configured allow-list of currencies, `PatronSubscriptionPaymentAsset`
wrapping a `CuratedToken` symbol, at its live USD rate via the existing
`rates.Provider`). The upstream two-call build-XDR-then-resubmit-with-
rollback-if-unsigned flow (an artifact of Stellar's signing model, not a
deliberate design) is replaced with a cleaner Build → Confirm split
matching the rest of this port: `BuildSubscription` validates and quotes
without persisting anything, `ConfirmSubscription` re-validates, records
the subscription log, and — only if the upgrade takes effect immediately —
the active membership row, once the buyer's self-submitted payment
confirms.

**Two bugs found while porting the activation worker, both fixed rather
than reproduced** (not just the one originally flagged):

1. Upstream's `UpdateUserPatronMemberships` runs once at process boot
   only — a subscription that becomes effective while the process keeps
   running past that point is never promoted until the next restart. This
   port calls `PromotePendingMemberships` from a proper recurring worker
   (main.go, 30s poll, matching the tokenization sales-activation cadence).
2. Upstream's own "find pending subscriptions to promote" query selects
   `effective_date >= now()` — backwards from what a "promote what's now
   due" sweep needs. As written, it promotes **future-dated** upgrades
   immediately (defeating the deferred-upgrade feature entirely) and stops
   selecting a log the moment its effective date has actually passed,
   meaning a genuinely-due promotion past its first sweep would never
   apply. This port selects `effective_date <= now()`, the correct
   direction, verified by `TestPromotePendingMemberships_PromotesDueLogsOnly`.

Verification: `go build`/`vet`/`gofmt` clean; 15 unit tests in
`internal/components/patron/services` covering the package/tier
upgrade-qualification matrix, the instant-vs-deferred effective-date
scheduling (new member, upgrade from a still-valid lower tier, an expired
membership renewing instantly, an existing lifetime membership always
activating instantly), pending-subscription rejection, and the activation
worker's due-vs-future selection.

### 4.11 Servicelinks partner API — **DONE**

Original: ~40 routes across two prefixes (`/v1/servicelinks/...`,
`/v1/trovo-api/...`), API-key-authenticated, covering login delegation
(request/approve/verify → issues the partner a JWT for that user), a
generic "authorize" 2FA-style deep-link flow, an "events" registration
flow, tokenized-asset info lookup, user-directory lookup, push-notification
relay, partner-driven user onboarding, KYC-status override, partner-driven
asset minting, sub-wallet creation, balance/payment-history lookup, a full
tokenization-management passthrough, and a separate private
stakeholder-document store. `ServiceLink` carries a capability bitset
(`LoginPermission`, `PaymentPermission`, `TokenInfoPermission`,
`AuthorizationPermission`, `EventPermission`, `PushNotificationPermission`,
`TokenizedAssetAuthorizationPermission`, `CreateUsersPermission`, plus
data-sharing flags).

**A full read of the original (`servicelinks/models`, `services`,
`controllers/main.go`, and all 4,969 lines of `controllers/
handlers_impl.go`) surfaced a cluster of real authorization bugs — this
section documents each one and how the Base port fixes it, since "port the
bug faithfully" is not an option for an auth/money-movement surface.**

**Bugs found and fixed (not reproduced):**

1. **API keys are stored and compared in plaintext** (`db.Where("api_key =
   ?", apiKey)`), no hashing anywhere. A single DB leak compromises every
   partner's credential. **Fixed**: `ServiceLink.APIKeyHash` stores only a
   SHA-256 hash; the raw key is shown to the partner exactly once, at
   creation, and never persisted.
2. **The API-key middleware attaches nothing to context** — every handler
   independently re-fetches the `ServiceLink` by re-extracting the header,
   and each hand-rolls its own permission/status checks (inconsistently —
   see next two findings). **Fixed**: `middleware.APIKeyAuth` resolves the
   `ServiceLink` once and attaches it to `gin.Context`; every handler reads
   it from there.
3. **Suspended/inactive/unverified service links are not blocked** by the
   primary auth path — only three stakeholder-document routes got a
   correct active/verified/non-suspended gate via a bespoke second
   middleware; the one function that implements the check correctly
   (`services.GetService`) is dead code, never called by any route.
   **Fixed**: `middleware.APIKeyAuth` itself rejects
   `Inactive`/`Suspended`/`!Verified` unconditionally, for every route,
   deny-by-default.
4. **`CreateUsersPermission` is a single flag gating the entire
   `/v1/trovo-api/*` surface** — onboarding, KYC override, minting,
   balance/history reads, payments, sub-wallets, and all tokenization-
   management routes share one boolean named for something else entirely.
   **Fixed**: split into granular capabilities — `CanCreateUsers`,
   `CanUpdateKYC`, `CanSendPayments`, `CanReadBalances`,
   `CanManageTokenization` — each independently grantable.
5. **No wallet-ownership scoping on money-moving/asset-moving routes.**
   The balance/history lookups correctly require
   `walletOwner.CreatedByServiceLinkID == callingServiceLink.ID`, but the
   payment-send, asset-mint, and primary-sale-purchase routes resolve the
   acting wallet from **unverified request headers/body fields** with no
   such check — any partner holding a valid API key with the (overloaded)
   permission could build/submit value-moving transactions against a
   wallet it doesn't own. Real signature verification deeper in the
   Stellar submission path stops actual fund loss, but the authorization
   boundary itself is the wrong shape to carry forward. **Fixed**: every
   route that acts on a specific user's wallet — payment, KYC override,
   tokenization actions — requires `wallet.CreatedByServiceLinkID ==
   serviceLink.ID`, checked once, centrally, before any service call.
6. **The KYC-override handler has a copy-paste bug** (`Username(kycData.
   KycStatus)` — casting the status *int* into the username lookup instead
   of the actual target-username field) **and**, independent of that bug,
   its ownership check only fires when `CreatedByServiceLinkID` is
   non-nil — skipping the check entirely for organically-registered users,
   so any partner could target arbitrary non-partner-created accounts.
   **Fixed**: look up by the real target identifier; reject unless
   `CreatedByServiceLinkID != nil && *CreatedByServiceLinkID ==
   serviceLink.ID` — deny-by-default, not deny-only-on-mismatch.
7. **The stakeholder-document store has no per-tenant ownership record** —
   any active, verified service link can read or delete *any other
   partner's* documents by GUID (an IDOR), since object identity is a bare
   UUID with no owning-service-link column. **Fixed**: every stored
   document row carries the uploading `ServiceLinkID`, checked on every
   read/delete.
8. **Three near-identical state machines** (login-delegation,
   generic-authorize, event-registration — each its own model, each its
   own request/approve/verify handler trio) exist upstream purely because
   they were built separately over time, not because the flows differ in
   any structural way. **Simplified, not just fixed**: one
   `ServiceLinkApproval` model with a `Kind` (`LOGIN`/`AUTHORIZE`/`EVENT`)
   discriminator and one set of request/approve/verify handlers serves all
   three — see below.
9. **The approval step's proof-of-authorization** (upstream: a fresh
   per-request Ed25519 signature over `path+signerPubkey+timestamp`,
   matched against the user's stored primary-signer key) has a direct,
   *simpler* equivalent already built into this port: every other route in
   this codebase replaced "sign every request" with SIWE-once-then-session-
   JWT (§2's very first substitution row). Reinventing a bespoke per-request
   signature scheme for just this one flow would be inconsistent with that
   decision for no security benefit. **The approval endpoint requires the
   user's own existing wallet-session JWT** (`middleware.JWTAuth(...,
   AudienceWalletSession)`) instead — the user must already be logged into
   their own wallet (exactly the state they're in when scanning a partner's
   QR code from within the wallet app) and the JWT's subject (their
   address) must match the pending approval's target address.
10. **Dead code, not ported**: `ServiceLinkApiKeyLog` (migrated, never
    read or written), the `IncludePhoneNumbers`/`IncludeUserBalances` flags
    (declared, never consulted anywhere — `ToServiceLinkUser()` always
    includes phone/email regardless), the duplicate payment-request route
    (`/v1/servicelinks/payment/request` and `/v1/trovo-api/payment/request`
    were byte-for-byte identical handlers), and the unauthenticated
    `token/refresh`/`token/verify` routes (no API-key check upstream
    either — not genuinely servicelinks-specific, and this port's session
    model has no refresh-token concept to begin with, per §2's SIWE→JWT
    row).

**Dropped outright, no Base equivalent (documented, not silently
skipped):**

- **`TokenizedAssetAuthorizationPermission`'s issuer-co-signs-a-client-XDR
  flow** (server co-signs a trustline/`AUTH_REQUIRED` authorization with
  the platform's Stellar issuer key). `TokenizedAsset.sol` (§4.9) has no
  on-chain compliance/authorization-required concept at all by design —
  "the chain enforces supply and ownership, the backend enforces who is
  allowed to end up holding it" — so there is nothing on-chain left for a
  partner to request authorization for.
- **The generic single-account asset-mint primitive**
  (`/v1/trovo-api/tokens/mint`, where any account holding an asset's
  issuer keypair could pay out unlimited credit of that asset). Base
  tokenized assets mint only through the ≥4-approver multisig workflow
  built in §4.9 — there is no "an account decides to mint" primitive to
  carry forward. Partner-driven minting in this port means participating
  in that same workflow via API key (`CanManageTokenization` gates calling
  `tokenization.Service.RequestMint`/`SignMintApproval` on the service
  link's own owner account), not a standalone mint call.

**What ports directly, chain-agnostically, as thin API-key-gated
passthroughs to services this plan already built:**

- Tokenized-asset info lookup, user-directory lookup (`userinfo`),
  push-notification relay.
- Partner-driven user onboarding (`CanCreateUsers`) — creates a
  `users.User` row exactly like normal registration, stamped
  `CreatedByServiceLinkID`.
- KYC-status override (`CanUpdateKYC`, fixed per finding 6 above).
- Balance / payment-history lookup (`CanReadBalances`) — the one part of
  the original that already scoped correctly; ported as-is.
- Partner-initiated payment (`CanSendPayments`) — reuses the existing
  payments Build/Submit pattern (§4.1), with ownership scoping added per
  finding 5.
- Sub-wallet registration — maps directly onto `users.UserWallet`
  (already-built "additional address" model, §4.1), registered for the
  service link's own owner account.
- The tokenization-management passthrough (apply, upload logo/documents,
  confirm application, confirm/acknowledge fees, admin/marketplace
  listing, deletion, primary-sale purchase) — thin wrappers over the
  already-built tokenization services (§4.9), with the service link's own
  owner user as the acting initiator/purchaser throughout, and
  ownership-scoping enforced uniformly per finding 5 rather than only on
  some routes.
- Stakeholder document store — separate object storage from tokenization's
  own document uploads, scoped by uploading `ServiceLinkID` per finding 7.

Implementation notes (what actually shipped, beyond the design above):

- `internal/components/servicelinks/models`: `ServiceLink` (the eleven
  granular `Can*` capability booleans from finding 4, plus
  `Verified`/`Inactive`/`Suspended`/`SuspensionReason`), `ServiceLinkApproval`
  (the `Kind`-discriminated login/authorize/event model from finding 8),
  `StakeholderDocument` (carrying `ServiceLinkID` per finding 7).
- `internal/middleware/api_key_auth.go`: `APIKeyAuth(db)` resolves the
  `ServiceLink` by `SHA256(rawKey)` once, attaches it to `gin.Context`
  under `CtxServiceLink`, and rejects `Inactive`/`Suspended`/`!Verified`
  centrally, for every route — findings 1–3.
- `internal/storage.Blob` gained a `Delete(key)` method (previously
  `Put`/`Get` only) so the stakeholder-document store can actually remove a
  deleted document's content, not just its DB row.
- `users.Service` gained three things this component needed:
  `RegisterInput.CreatedByServiceLinkID` (threaded into `models.User` at
  creation), `GetByID` (every `requireOwnedUser` check starts here), and
  `SetKYCVerifiedLevel`/`RegisterWallet` (the KYC-override and sub-wallet
  passthroughs). `payments`/`users`/`assets` controllers' `Init` functions
  now all return their `*services.Service`, matching the pattern
  tokenization/patron's controllers already established, so `main.go` can
  wire them into servicelinks without a cross-component import cycle.
- Unlike every other cross-component link in this port (KYC→Stablerail's
  `OnBVNVerified`, fiat→tokenization's `CreateFiatInvoiceFunc` — function-
  field callbacks, used specifically so two *peer* components never import
  each other), `servicelinks/services.Service` imports
  `users`/`payments`/`assets`/`tokenization`'s services packages directly.
  This is a deliberate departure, documented in the package's own doc
  comment: servicelinks is architecturally a facade/gateway in front of
  all four, not a peer of any of them, so a callback hook for every one of
  the dozen calls it needs would add indirection with no benefit.
- `requireOwnedUser(serviceLinkID, userID)` is the one deny-by-default
  check every route touching a specific user's wallet goes through
  (finding 5/6): it rejects unless `user.CreatedByServiceLinkID != nil &&
  *user.CreatedByServiceLinkID == serviceLinkID` — a nil value is *always*
  rejected, never treated as "skip the check" the way the original's
  KYC-override handler did.
- The consolidated `ServiceLinkApproval` flow (finding 8) splits across the
  API-key-authenticated partner side (`RequestApproval`, gated per `Kind`
  by `CanLogin`/`CanRequestAuthorization`/`CanRegisterEvents`; `VerifyApproval`,
  single-use — the approval row is deleted on redemption, closing off
  replaying the same approval ID for a second session token) and the
  wallet-session-JWT-authenticated user side (`GetApproval`, `Approve` —
  both require the caller's verified session address to match the
  approval's target user, per finding 9's SIWE-session substitution rather
  than a bespoke signature scheme). A `LOGIN`-kind approval's `VerifyApproval`
  mints a wallet-session JWT for the target user via the existing
  `middleware.IssueToken`; `AUTHORIZE`/`EVENT` approvals return no token —
  the partner's next call still goes through `requireOwnedUser` regardless
  of what was approved.
- Partner-onboarded users (`OnboardUser`) are a documented, deliberate
  exception to `users.Service.Register`'s normal SIWE-proof-of-address-
  ownership rule: the address comes from the partner's request body, not a
  verified session, because a partner-onboarded user's wallet is
  provisioned and held by the partner's own embedded-wallet
  infrastructure — there is no SIWE session with this API to prove
  ownership with. The trust boundary is `CanCreateUsers` plus the calling
  service link being `Verified`, not a wallet signature.
- `sharedconfig.GlobalConfig` gained `ServiceLinkApprovalTTL`
  (`SERVICELINK_APPROVAL_TTL_MINUTES`, default 10) for how long a pending
  approval stays actionable before it expires unredeemed.
- Verification: `go build`/`vet`/`gofmt` clean across the whole module; 16
  unit tests in `internal/components/servicelinks/services` (service-link
  provisioning and its unique-short-name conflict, `requireOwnedUser`'s
  three cases — organic user, wrong owner, correct owner — the KYC-override
  ownership fix, the full login-approval flow end-to-end including the
  minted JWT's subject and single-use redemption, wrong-caller and
  wrong-service-link rejection, and the stakeholder-document store's
  per-tenant isolation on both read and delete) plus 7 in
  `internal/middleware` covering `APIKeyAuth`'s missing/unknown/unverified/
  inactive/suspended rejection paths and that the raw key never equals its
  stored hash.

### 4.12 Admin surface ("Trovo Manager" equivalent) — **DONE**

Original: JWT-authenticated admin routes for tokenization vetting/minting/
fee-acknowledgement/sales-date management/deletion, wallet-balance lookup,
and (implicitly, via the models) KYC-config and country-config management.
Currently this codebase only has one example admin route
(`POST /v1/admin/announcements`).

Base design: chain-agnostic — extends the existing `middleware.JWTAuth(...,
middleware.AudienceAdmin)` pattern to a proper admin route group per
subsystem (tokenization vetting/minting, KYC config, patron config, wallet
lookup), rather than inventing a new auth mechanism.

Implementation notes (what actually shipped):

- Tokenization vetting/minting/fee-acknowledgement/sales-date-management/
  deletion admin routes already exist under `/v1/admin/tokenization/...`,
  built ad hoc in Phase 9 (see §4.9's "Routes" note) since that subsystem
  had no partner-API caller yet to justify deferring them — nothing new
  needed here beyond the `middleware.AudienceAdmin` pattern they already
  established, which every admin route added in this phase follows too.
- `internal/components/kyc/services/admin.go` +
  `internal/components/kyc/controllers/admin.go`: CRUD on
  `models.SumsubLevel` (`POST`/`PUT`/`DELETE
  /v1/admin/kyc/sumsub/levels[/:id]`) and `models.DojaWidget`
  (`.../doja/widgets[/:id]`) — the two vendor-dashboard-mirroring catalogs
  an operator needs to keep in sync with their actual Sumsub/Dojah
  configuration (see those models' doc comments for why there's no way to
  discover them via either vendor's API).
- `internal/components/patron/services/admin.go` +
  `internal/components/patron/controllers/admin.go`: CRUD on
  `PatronPackage`/`PatronTier` (create, toggle `Inactive`, delete),
  `UpsertMembershipGrade` (create-or-reprice a package+tier's USD price,
  rejecting an unknown package/tier id), and
  `SetPaymentAssetAllowed` (add/disable a subscription payment-currency
  symbol) — all under `/v1/admin/patron/...`. This replaces upstream's
  implicit "edit the seed data / dashboard-less config" story for the
  three fixed packages/tiers with actual admin endpoints, still built
  around the same hardcoded three-package/three-tier catalog `main.go`
  seeds on first boot (§4.10) rather than opening the catalog shape itself
  up to arbitrary admin changes.
- `internal/components/assets/controllers`: one new admin route,
  `GET /v1/admin/wallet/:address/balance` — the wallet-balance-lookup tool
  from the original's admin surface, reusing the exact same
  `assets.Service.Balance` (and its request handler) the public/authed
  balance routes already use, just gated by `AudienceAdmin` instead of a
  per-caller address restriction, since an admin needs to look up *any*
  address's balance, not just their own.
- No new component or model package was needed for any of this — every
  addition is either a new admin-only method on an existing component's
  `Service`, or a route registered in that component's own `controllers`
  package, consistent with how tokenization's admin routes were already
  built in Phase 9.
- Verification: `go build`/`vet`/`gofmt` clean across the whole module; 10
  new unit tests (4 in `internal/components/kyc/services` covering
  Sumsub-level/Doja-widget create-conflict, update, and delete-then-404;
  6 in `internal/components/patron/services` covering package/tier
  create-conflict and lifecycle, membership-grade unknown-package/tier
  rejection and create-then-reprice-in-place, and payment-asset
  create-then-toggle).

### 4.13 Reference data & misc — **DONE**

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

Implementation notes (what actually shipped):

- **Banks**: already ported under a different name in Phase 5 —
  `stablerail.StablerailBank`, synced from the Stablerail API
  (`SyncSupportedBanks`). Nothing further needed here; upstream's generic
  `Bank` table and Stablerail's own bank list were the same concept
  duplicated, and this port only carries the one it actually uses.
- **Reserved usernames**: already ported in Phase 2 (`users.ReservedName`).
  Nothing further needed.
- `internal/components/reference` (new): `Country`/`CountryConfig` (the
  per-country fee/activation/regulator/high-risk config `fiat.
  ActivationConfig`'s own doc comment deferred to this phase) and
  `JsonForm` (a versioned, client-rendered form definition, `UpsertForm`
  bumping `Version` on every update so a client can detect a stale cached
  copy). Public read routes (`/v1/reference/...`) plus an admin CRUD
  surface (`/v1/admin/reference/...`), the same public-read/admin-write
  split as `announcements`.
- `internal/geoip` (new): a `Provider` interface (`Lookup(ctx, ip)
  (countryCode, err)`) with `NoopProvider` as the default and
  `IPAPIProvider` hitting ipapi.co's free plain-text endpoint as the real
  implementation — same "free/local default, real vendor optional"
  pattern as `kyc.Provider`/`fiat.Processor`. `users.Service` gained a
  `GeoIP` field (defaulting to `NoopProvider`) and
  `RegisterInput.RegistrationIP`; `Register` now sets
  `User.RegistrationCountryCode` and, if that country has a
  `reference.CountryConfig` row marked `HighRisk`, `RegistrationHighRisk`
  — both purely advisory (a lookup failure never blocks registration) and
  read via a direct query against `reference`'s `CountryConfig` table
  (the same shared-DB cross-component pattern tokenization's own
  `curatedToken()` helper uses against `assets.CuratedToken`, rather than
  a new cross-component hook). `users.Service.Register` reads the caller's
  IP from `RegisterInput.RegistrationIP`, which `controllers.register`
  wires to `c.ClientIP()` — never a client-supplied header.
- Discord alerting parity: `payments.Service` and `swaps.Service` both
  gained an `Alerts alerting.Notifier` field (defaulting to
  `NoopNotifier`, wired to the real one post-construction in `main.go` -
  same pattern as `users.Service.GeoIP`, chosen so every existing
  `New(...)` call site keeps working unchanged) and now alert on every
  rejected submission. This deliberately does not try to distinguish a
  user error (bad nonce, insufficient balance) from an infra failure (RPC
  unreachable) - matching the original's own behavior of alerting on every
  rejected submission, and there being no reliable way to tell the two
  apart from `SubmitSignedTransaction`'s single wrapped error string
  without deeper geth-error-code inspection that was out of scope here.
  `fiat.Service` gained the same `Alerts` field (alerting on a failed
  faucet dispense) plus `FaucetAddress()` and a separate proactive
  `CheckFaucetBalance` (opt-in via `FAUCET_LOW_BALANCE_THRESHOLD_ETH`,
  polled every 30 minutes from `main.go` only when configured) - a
  low-balance warning is a different signal from a dispense actually
  failing, so it's a second alert path, not a reuse of the first.
  `internal/db` gained `SetPoolLimits`/`PoolStats` (the pool was
  previously unbounded with no exhaustion signal at all); `main.go` polls
  `PoolStats` every minute and alerts if `InUse` saturates
  `DB_MAX_OPEN_CONNS` or `WaitCount` has grown since the last check
  (callers had to wait for a connection).
- `internal/components/shortlink` (new): `DynamicLink{ID, ShortCode,
  TargetURL, Metadata, ClickCount}`, an 8-character Crockford-base32 short
  code (unambiguous characters, easy to retype from a printed QR code),
  `github.com/skip2/go-qrcode` for PNG rendering. `POST /v1/shortlinks`
  (wallet-session-authenticated - minting a link is an authenticated
  action) creates a link; `GET /s/:code` (public) redirects and records a
  click; `GET /s/:code/qr` (public) serves its QR code as a PNG, looked up
  via a separate non-click-counting `GetLink` so viewing a QR image is
  never itself counted as a visit to the link.
- One real bug caught by this phase's own test suite: `Resolve`'s
  click-increment used GORM's `UpdateColumn` with a raw `click_count + 1`
  SQL expression, which updates the database row correctly but does *not*
  write the resulting value back into the Go struct - the method was
  returning the pre-increment count to its caller. Fixed by reloading the
  row after the update; caught by
  `TestResolve_IncrementsClickCount` expecting `2` after two resolves and
  getting `1`.
- Verification: `go build`/`vet`/`gofmt` clean across the whole module; 7
  new unit tests in `internal/components/reference/services` (country
  upsert/validation, nil-config-means-not-high-risk, form
  version-bumping and deactivation), 5 in `internal/geoip` (noop, a real
  HTTP round-trip against an `httptest.Server`, private-address
  short-circuiting, a non-code vendor response treated as unknown, HTTP
  error propagation), 3 in `internal/components/users/services`
  (country-code/high-risk field population, and that a geo-IP failure
  never blocks registration), 5 in `internal/components/fiat/services`
  (dispense-failure alerting, the faucet low-balance check's three cases,
  `FaucetAddress` determinism), 1 in `internal/db` (pool limits actually
  take effect), and 7 in `internal/components/shortlink/services`
  (short-code uniqueness/length, click counting vs. non-counting lookup,
  unknown-code rejection, short-URL construction, PNG output).

## 5. New infrastructure this port requires that didn't exist before

- **A minimal Solidity ERC-20 contract** (mintable, burnable, ownable),
  used once per tokenized asset (§4.9) and, if needed, for any
  Trovo-issued reward/utility token. **DONE** —
  `internal/contracts/solidity/TokenizedAsset.sol` (OpenZeppelin
  `ERC20Burnable`+`Ownable`, custom `decimals()`, `mint` restricted to the
  owner), compiled ahead of time with solc 0.8.24 (never at runtime; see
  `solidity/README.md` for the reproducible build) and embedded as
  ABI+bytecode via `//go:embed` in `internal/contracts`. Deployed through
  `network.Client.DeployContract`, a hand-rolled `types.DynamicFeeTx` with
  `To: nil` built the same way every other transaction in this codebase
  is — not `go-ethereum/accounts/abi/bind`'s generated-binding
  `DeployContract`, which would have introduced a second, inconsistent way
  of building transactions alongside `internal/network`'s existing
  build/sign/submit machinery. The deploying/mint-authority key comes from
  `cryptoutil.DeriveKey` per §2, as originally planned.
- **A minimal `Sale.sol` contract** for atomic primary-sale purchases
  (§4.9), so a buyer's approve+buy stays a predictable two-step flow
  instead of a bespoke multi-transaction dance per asset. **DONE** —
  `internal/contracts/solidity/Sale.sol` (`Ownable`, `buy`/`setPaused`/
  `withdrawUnsold`), same compile/embed/deploy path as TokenizedAsset
  above.
- **On-chain price reading** (Uniswap V3 pool `slot0`) added to
  `internal/network`, for the assets order-book/price-discovery
  replacement (§2). **DONE** — `internal/network/price.go`:
  `GetPoolState` reads `slot0`/`token0`/`token1` via three `eth_call`s
  against a minimal embedded pool ABI, and `PoolPrice` turns
  `sqrtPriceX96` into a human price adjusted for both tokens' decimals.
- **A chain-log polling worker** replacing Horizon operation streaming
  for cache invalidation (§2). **DONE** —
  `internal/network/watcher.go`'s `AddressWatcher` polls `eth_getLogs`
  for `Transfer`/`Approval` logs over the block range since its last pass
  (main.go runs it every ~4s, roughly two Base blocks) and fires a
  one-shot callback for each address a matching log touches. Wired into
  the one response this port currently caches by address —
  `GET /v1/users/:username` (`internal/components/users/controllers`) —
  which registers a cache-delete callback per request alongside a 5-minute
  TTL backstop in case a poll is ever missed. Any future component that
  caches something keyed by an on-chain address can reuse the same
  `GlobalConfig.AddressWatcher` rather than inventing its own
  invalidation path.
- **An API-key auth middleware** for the servicelinks partner surface
  (§4.11), alongside the existing SIWE/JWT middleware. Deferred to Phase
  11 (§10) — it has no caller until the servicelinks component exists, so
  building it now would mean designing its key-scoping/rate-limit shape
  without the concrete endpoints it needs to protect.
- **A self-hosted shortlink + QR service** replacing Firebase Dynamic
  Links (§4.13). Deferred to Phase 13 (§10) for the same reason — it
  belongs with the reference-data/shortlinks work it's part of.

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

**As actually built** (see README's "Project layout" for the reader-facing
version of this same tree). A few names changed from the original plan
below during implementation: `recovery` was folded into `users` (§4.3),
`marketmaking` shipped as `market` (§4.8), and there is no standalone
`admin` component — each subsystem's admin routes live in that
subsystem's own `controllers` package under `/v1/admin/...`, gated by
`middleware.AudienceAdmin` (§4.12).

```
wallet-backend/
├── main.go
├── contracts/                      # Solidity sources + compiled ABI/bytecode (§5)
│   ├── TokenizedAsset.sol
│   └── Sale.sol
├── internal/
│   ├── sharedconfig/  db/  cache/  apperrors/  cryptoutil/  validators/
│   ├── network/                     # + on-chain price reads, log polling, SignAndSubmitTx
│   ├── middleware/                   # + APIKeyAuth for servicelinks
│   ├── notify/ storage/ rates/ alerting/          # + geoip (§4.13)
│   ├── kyc/                          # SumsubProvider-shaped REST client, DojaProvider webhook processing
│   ├── fiat/                         # Processor interface; FlutterwaveProcessor implements it
│   ├── contracts/                    # embedded Solidity ABI/bytecode + deployment helpers (§5)
│   └── components/
│       ├── root/ announcements/ callbacks/
│       ├── auth/ users/ assets/ payments/ swaps/             # §4.1
│       ├── sharedaccess/             # §4.2
│       ├── kyc/                      # §4.4 — HTTP layer over internal/kyc
│       ├── fiat/                     # §4.5 — HTTP layer over internal/fiat
│       ├── stablerail/               # §4.6
│       ├── crypto/                   # §4.7 — deposit/withdrawal
│       ├── market/                   # §4.8
│       ├── tokenization/             # §4.9 — the big one
│       ├── patron/                   # §4.10
│       ├── servicelinks/             # §4.11
│       ├── reference/                # §4.13 — country catalog/config, dynamic forms
│       └── shortlink/                # §4.13 — self-hosted dynamic-link + QR replacement
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
| 2 | Account security & recovery (§4.3) | Phase 0 | **DONE** |
| 3 | KYC — Sumsub + Doja (§4.4) | Phase 0 | **DONE** |
| 4 | Fiat payments & activation — Flutterwave (§4.5) | Phase 0, benefits from Phase 3 (activation often gated on KYC) | **DONE** |
| 5 | Stablerail (§4.6) | Phase 3 (BVN/KYC-triggered onboarding) | **DONE** |
| 6 | Crypto deposit/withdrawal — OneLiquidity (§4.7) | §5's contract-deployment infra (for the mint side) | **DONE** (via treasury transfer, not mint — see §4.7) |
| 7 | Market making (§4.8) | §11's design decision | **DONE** |
| 8 | On-chain infra: Solidity contracts + deployment helper, price reading, log polling (§5) | Needed before Phase 9 | **DONE** |
| 9 | Tokenization (§4.9) | Phase 8, Phase 1 (closed-group reuse), Phase 4 (fiat purchase flow) | **DONE** (partner-API passthrough deferred to Phase 11 — see §4.9) |
| 10 | Patron/membership (§4.10) | Phase 0 | **DONE** |
| 11 | Servicelinks partner API (§4.11), including the API-key auth middleware (§5) | Nearly everything above, since it's a passthrough layer | **DONE** |
| 12 | Admin surface (§4.12) | Whatever subsystems exist by then | **DONE** |
| 13 | Reference data, shortlinks, geo-IP, Discord alerting parity (§4.13) | Can run in parallel with any phase | **DONE** |
| 14 | Full integration pass: build/vet/test, smoke test against Base Sepolia, README/docs polish | Everything | **DONE** |

Phase 14 verification: `gofmt`/`go build`/`go vet`/`go mod tidy` clean
across the whole module; `go test ./... -race` passes with no failures
(the `covdata` warnings some no-test-file packages emit under
`-coverprofile` are a sandbox toolchain quirk, not a test failure - every
package that has tests reports `ok`). The full binary was booted against
the live public Base Sepolia RPC end-to-end: `GET /` reported
`{"status":"ok","chainId":84532}` (live chain connectivity), SIWE nonce
issuance, the patron/reference and country-catalog public routes, and
every admin/API-key/wallet-session-gated route checked all rejected an
unauthenticated request with 401 as expected. README's project layout,
auth section, and component list were brought up to date to list every
component actually built (previously stale from the very first, much
smaller revision of this plan).

## 11. Open decisions needing input before implementation proceeds

- **Market making design (§4.8)**: off-chain server-matched order book vs.
  swap-only. **Resolved — off-chain server-matched order book chosen**;
  see §4.8 for the implementation, including the escrow-based settlement
  mechanism this required.
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

## 12. Authentication redesign: header-based per-request signatures (supersedes §2's first row)

**Decision**: replace SIWE+session-JWT with the original's per-request
signature model for every genuinely user-signed operation, restoring the
original's exact behavioral shape — one signature per request, no session
token — on Base's cryptography.

### 12.1 What the original actually does (audited directly, not assumed)

`trovo-wallet-monorepo/backend/internal/middleware/authentication_middleware.go`'s
`AuthenticationMiddlewareUsingTimestamp()`, used on essentially every route
in that codebase:

- **Headers**: `X-TW-SIGNER` (the signer's Stellar public key),
  `X-TW-SIGNATURE` (base64 ed25519 signature), `X-TW-TIMESTAMP`. Separately,
  `X-TW-PUBLIC-KEY` — **the wallet the request acts on** — is read directly
  by each handler (`middleware.ExtractPublicKey(c)`), entirely outside the
  auth check itself.
- **Message signed**: `fullPathWithQuery + signerPublicKey + timestamp`,
  raw concatenation, ed25519-signed (`security_checks.go`).
- **The middleware only proves who signed.** Authorization — may this
  signer act on this wallet — is a separate, handler-level concern:
  self-service when `wallet == signer`, or a shared-access permission
  record (`Permission == "INITIATOR"` etc.) when acting for someone else.
- **Confirmed gap, not reproduced**: there is no timestamp-freshness check
  anywhere in this code path. A captured `(path, signer, timestamp,
  signature)` tuple is replayable **indefinitely**, not just within some
  window — worse than the "just a timestamp, not a nonce" characterization
  in §2's original row implied. §12.3 fixes this with a cheap, stateless
  tolerance window.
- **Servicelinks partner routes used the same middleware, plus a second,
  independent check**: a service link has its own Stellar signer identity
  *and* a separately presented `X-TW-SERVICE-LINK-API-KEY`, both required.
  This port's existing `internal/middleware/api_key_auth.go` (hashed API
  key, resolved `ServiceLink`, deny-by-default on
  inactive/suspended/unverified) already covers this identification job on
  its own terms and is **not being replaced** — see §12.5.

### 12.2 Base equivalent

| Original | Base | Carries |
|---|---|---|
| `X-TW-SIGNER` | `X-Signer-Address` | the EVM address that produced the signature |
| `X-TW-PUBLIC-KEY` | `X-Wallet-Address` | the wallet address the request acts on (self, or a delegated one) |
| `X-TW-SIGNATURE` | `X-Signature` | `0x`-prefixed EIP-191 personal_sign signature (65 bytes) |
| `X-TW-TIMESTAMP` | `X-Timestamp` | Unix seconds |

`internal/middleware/cors.go` already allowlists `X-Public-Key,
X-Timestamp, X-Signature` — a scaffold anticipating close to this exact
scheme that was never wired up. On Base an address is not a public key
(it's `keccak256(pubkey)[12:]`), so "public key" terminology is retired
entirely rather than kept as a fallback name: the allowlist entry becomes
`X-Wallet-Address` (replacing `X-Public-Key`), and `X-Signer-Address` is
newly added alongside it.

**Message signed**: identical shape, `fullPathWithQuery + signerAddress +
timestamp`, personal_sign in place of raw ed25519.

**Verification**: `internal/cryptoutil.VerifyPersonalSign(message string,
signature []byte, expectedAddress common.Address) (bool, error)` **already
exists** (built for shared-access's off-chain approval signatures,
`accounts.TextHash` + `crypto.SigToPub` + `crypto.PubkeyToAddress`, v-byte
normalized) — the new middleware calls this directly, hex-decoding
`X-Signature` into 65 raw bytes first. No new cryptographic code is
needed, only the header extraction, timestamp check, and context wiring
around this existing helper.

### 12.3 The one deliberate deviation: timestamp tolerance

New config: `REQUEST_SIGNATURE_TOLERANCE_SECONDS` (default 300). The
middleware rejects a request if `|now - X-Timestamp| > tolerance`, checked
*before* signature verification (cheap early exit). This is the fix for
§12.1's confirmed unbounded-replay gap - stateless (no server-side nonce
cache, unlike SIWE's), and small enough that a legitimate client's own
clock skew is the only practical failure mode to size it against.

### 12.4 Authorization stays handler-level, and mostly stays put

The new middleware sets `middleware.CtxSubject` to the **wallet** address
(the value every existing handler already reads via
`c.GetString(middleware.CtxSubject)`) once it's confirmed the signer may
act on it — self-service (`wallet == signer`) needs no further check;
acting on a different wallet requires a `sharedaccess` `GroupMember` role
granting it, mirroring the original's own permission check exactly. This
keeps the ~14 files currently reading `CtxSubject`
(`users`/`payments`/`assets`/`swaps`/`sharedaccess`/`kyc`/`fiat`/
`stablerail`/`crypto`/`patron`/`tokenization`/`market` controllers)
**unchanged** for the common case - only the middleware producing that
value changes, not the handlers consuming it. A second context key
(`CtxSigner`) is set alongside it for the delegated case, for any handler
that needs to distinguish "acting for myself" from "acting as an approved
delegate" (audit logging, restricting a VIEW_ONLY delegate from
mutating routes at the handler level).

### 12.5 What gets removed, what doesn't

**Removed**: `internal/components/auth` (SIWE nonce/verify service +
controller) entirely; `middleware.AudienceWalletSession`,
`middleware.IssueToken`'s use for it, and `JWTAuth(...,
AudienceWalletSession)` on every currently-JWT-guarded user route; the
`siwe-go` dependency; `SIWE_DOMAIN`/wallet-session `JWT_EXPIRY_MINUTES`
config.

**Not removed** (out of this decision's scope - "user operations," not
every caller class):
- `middleware.AudienceAdmin` JWT for staff/admin routes - unaffected.
- `middleware.APIKeyAuth` for servicelinks partner routes - unaffected.
  The original layered its universal signature scheme *and* a
  service-link API key on these routes; this port's API-key-only scheme
  already identifies "which partner" on its own terms without needing a
  partner-held signer keypair at all, and is arguably the cleaner of the
  two designs already in place - not being undone just to match the
  original's blanket middleware application.

**Correction after auditing the original's actual servicelinks code (§14
below did this properly - the assessment below supersedes what an earlier
draft of this section assumed without having read that code yet)**:
`servicelinks/services/approvals.go`'s `VerifyApproval` currently mints a
session JWT only for a `LOGIN`-kind approval, and returns just the
approval record (no token) for `AUTHORIZE`/`EVENT` - its own doc comment
frames this as a departure from "the original's bespoke per-request
Ed25519 signature scheme." Having now read the original's actual
`getServicelinksLoginVerify...` handler, **this port's current behavior
is already correct, not a bug**: the original's own LOGIN-verify calls
`LogUserIn`, which issues exactly this kind of access/refresh token pair
to the requesting partner; its AUTHORIZE-verify returns only
`{"message": "success"}`, no token, exactly matching what this port does
today. **No code change is needed here.**

The one real follow-up this section's redesign creates: once
`AudienceWalletSession` is removed (§12.5 above), the constant this
LOGIN-verify token currently rides on disappears, but the token itself
must not - it authenticates a *partner*, not the wallet's own end-user
API calls, so it isn't touched by "no more session tokens for user
operations." Phase 4 below is now: give this one remaining token issuer
its own explicit audience (e.g. `AudienceServiceLinkSession`) instead of
quietly inheriting a name that no longer means anything once every other
`AudienceWalletSession` issuer is gone - a naming cleanup, not a behavior
change. See §14.3 for the rest of servicelinks' parity gaps (QR
generation, callback dispatch), which are unrelated to this auth
redesign and don't block it either way.

### 12.6 `wallet-web` impact (tracked here, implemented there)

This is the same kind of cross-project dependency `wallet-payment-
history-engine/PLAN.md` and `wallet-web/PLAN.md` each flagged for their
own backend requirements - noted here, addressed in `wallet-web`'s own
plan/implementation:

- Remove: `api/siwe.ts`, `api/authFlow.ts`'s SIWE-specific functions,
  `api/authApi.ts`'s nonce/verify calls, `authSlice.ts`'s `sessionToken`
  (no bearer token to store) - and the onboarding wizard's separate
  "sign in" step, since there's no login round trip left to do.
- Add: a per-request signing step in `api/httpClient.ts` - build
  `fullPathWithQuery + signerAddress + timestamp`, sign it with the
  **signer** role's key (`wallet-core`'s `sign_siwe_message` is already
  message-agnostic EIP-191 personal_sign despite its name - reuse or
  rename it), attach the four headers to every authenticated call.
  Registration (`POST /v1/users`) becomes just another signed request
  with no separate auth step first.
- **A property being traded away, stated plainly**: SIWE's `domain` field
  is a real phishing signal - a signing wallet can show "you are signing
  in to wallet.example.com." A generic path+address+timestamp message has
  no domain binding at all. This doesn't matter for `wallet-web`'s own
  embedded signer specifically (it signs silently in a Worker with no
  human-reviewed prompt either way - see that project's own PLAN.md §4.1),
  but is worth remembering if a browser-extension-wallet client (MetaMask
  et al.) is ever added against this same API, since that class of client
  *does* show the user what they're signing, and an unreadable
  path-string message is a materially worse thing to show them than a
  formatted SIWE message would have been.

### 12.7 Phased build roadmap for this change

| Phase | Scope | Depends on |
|---|---|---|
| 1 | `internal/middleware/signature_auth.go`: header extraction, timestamp tolerance, `cryptoutil.VerifyPersonalSign` call, self-service authorization, `CtxSubject`/`CtxSigner` wiring | — |
| 2 | Delegated authorization: `sharedaccess` `GroupMember` role check when `wallet != signer` | Phase 1 |
| 3 | Wire the new middleware onto every route currently guarded by `JWTAuth(..., AudienceWalletSession)`; remove `internal/components/auth`, `siwe-go`, related config | Phase 1, 2 |
| 4 | Give the servicelinks LOGIN-verify token its own `AudienceServiceLinkSession` audience per §12.5 (no behavior change - `VerifyApproval` itself is already correct) | Phase 3 |
| 5 | Update `.env.example`/`DEPLOYMENT.md` for the new config, remove the retired SIWE ones | Phase 3, 4 |
| 6 | Full build/vet/test/tidy pass; a live smoke test exercising a signed request end-to-end | Everything above |
| 7 (tracked, not implemented here) | `wallet-web`'s own client-side changes (§12.6) | This phase's completion |

Each phase, when implementation is authorized, follows this project's
established discipline: build clean → test → document in this file's own
"Implementation notes" → commit and push. Implementation does not begin
until explicitly authorized.

Implementation notes (what actually shipped, beyond the design above):

- Phases 1-6 are done, in one pass. `internal/middleware/signature_auth.go`
  is exactly the design in §12.1-§12.4: header extraction
  (`X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp`),
  the tolerance-window check (`SignatureAuthToleranceSeconds`, default
  300s via `SIGNATURE_AUTH_TOLERANCE_SECONDS`), offline verification via
  the already-existing `cryptoutil.VerifyPersonalSign`, self-service
  authorization when signer and wallet match, and delegated
  authorization via a case-insensitive `sharedaccess` `ClosedGroup`/
  `GroupMember` lookup when they don't (any role, including `VIEW_ONLY`,
  passes the middleware - the finer per-role distinction stays a
  handler-level concern, unchanged). Sets both `CtxSubject` (the wallet
  acted on) and the new `CtxSigner` (who actually signed).
- All 14 routes previously guarded by `JWTAuth(gc.JWTSecret,
  middleware.AudienceWalletSession)` now use `SignatureAuth(gc.DB,
  gc.SignatureAuthToleranceSeconds)` instead - a mechanical, one-line
  swap per file, since every handler already read the wallet address
  from `CtxSubject` and needed no changes.
- `internal/components/auth` (the SIWE nonce/verify component) is
  deleted outright, along with the `github.com/spruceid/siwe-go`
  dependency and its two transitive-only deps (confirmed via `go mod
  tidy`). `middleware.AudienceWalletSession` is removed from
  `jwt_auth.go` - nothing issues or guards it anymore.
- Phase 4 (`AudienceServiceLinkSession`) is a rename only -
  `servicelinks/services/approvals.go`'s `VerifyApproval` was already
  behaviorally correct (§12.5), so this just gives its `LOGIN`-kind
  token issuance its own audience distinct from the now-removed
  wallet-session one.
- `internal/middleware/cors.go`'s allowed-headers list swapped
  `X-Public-Key` for `X-Signer-Address`/`X-Wallet-Address`.
- `.env.example`, `DEPLOYMENT.md`, and `README.md` all updated - the
  `SIWE_DOMAIN` var is gone, `SIGNATURE_AUTH_TOLERANCE_SECONDS` documented
  in its place, and every doc comment/README passage describing the old
  SIWE-then-JWT flow rewritten to describe the new one (README's flow
  previously named exact now-deleted endpoints, `/v1/auth/nonce` and
  `/v1/auth/verify` - left uncorrected, that would have been actively
  wrong documentation, not just stale).
- New `internal/middleware/signature_auth_test.go`: missing headers,
  tampered signature, wrong-key signature, stale timestamp, future
  timestamp beyond tolerance, self-service success, delegated success
  (a signer holding `GroupMember` standing on a different wallet), and
  delegated rejection (no standing) - all using real generated
  secp256k1 keys and real `personal_sign` signatures through the actual
  `gin` handler chain, not mocks.
- Full `go build`/`go vet`/`go test ./...` pass clean; a live server
  boot (`go run .`) confirmed the route table no longer has any
  `/v1/auth/*` routes and every other route is wired as expected.
- Phase 7 (`wallet-web`'s client-side changes, §12.6) remains tracked
  there, not implemented here, per that project's own `PLAN.md`.

## 13. Multi-wallet & shared-access redesign: smart-contract accounts for sub-wallets

**Decision**: replace Stellar's native weighted-signer accounts (used for
both sub-wallet ownership and shared-access authorization) with Base
smart-contract accounts (Safe), and replace the already-built
`sharedaccess` component's server-held-key design with real on-chain
multisig enforcement.

### 13.1 What the original actually does (audited directly)

**Sub-wallet creation** (`internal/components/users/services/
subwallets.go`, audited in full):
- `users`/`user_wallets` schema: `User.PublicKey`/`PrimarySigner` (both
  set to the same address at registration); `UserWallet.ID` (the wallet's
  own address), `.Signer` (whoever controls it - **the row's own doc
  comment says "if ID is same as Signer, it's a primary wallet"**),
  `.PrimaryWallet` (an explicit redundant flag), `.SharedAccessEnabled`,
  `.NumberOfApprovalsNeeded` (the threshold), `.Permissions
  []WalletPermission` (the approver/initiator/viewer list - approver
  *count* is derived by filtering this, never stored as its own column).
- Sub-wallet creation is a two-phase call to the same endpoint
  (`POST /v1/users/subwallet`): phase 1 (no signatures) returns an
  unsigned XDR that (a) `CreateAccount`s the new address funded from the
  primary's balance (default 6 XLM, DB-configurable per wallet type via
  an `ActivationAmount` table, gated by a `minBalance` floor - default 3,
  env-overridable), and (b) `SetOptions{Signer: primary, Weight: 1}` on
  the new account. Phase 2 (with `PrimarySignature` + `SubWalletSignature`)
  submits it; `txnbuild.AddSignatureBase64` cryptographically validates
  each signature before submission, and the DB row is inserted inside a
  transaction that's rolled back if submission fails.
- **Correction to this design's original working assumption**: for an
  ordinary (`WalletType == 0`) sub-wallet, the original does **not**
  actually revoke the sub-wallet's own signing weight - it only *adds*
  the primary as an additional weight-1 signer alongside the sub-wallet's
  untouched default weight-1 key. Nothing on-chain stops that discarded
  key from signing its own account's transactions later; the "operated
  only via the primary" property is a client/backend convention (the app
  discards the key after the one co-signing call), not an on-chain
  guarantee. Only the two custodial types - market-making and
  bulk-payment - actually raise `LowThreshold`/`MediumThreshold`/
  `HighThreshold` to 3 with a weight-3 custodial signer, which functionally
  outranks the sub-wallet's own weight-1 key. Worth knowing before treating
  "the original locks out the sub-wallet's own key" as ground truth to
  replicate for ordinary sub-wallets - it doesn't, for that path.

**Shared access** (`users/services/shared_access.go`,
`users/models/user.go`, audited in full):
- Permission is a free-text column (no Go enum), three literal values in
  use: `"VIEW-ONLY"`, `"INITIATOR"`, `"APPROVER"` (the user's own message
  calls the third one "AUTHORIZER" - same role, different word; worth
  deciding whether to rename in the port, purely cosmetic either way).
- `CreateSharedWalletAccess`/`ModifySharedWalletAccess` build real
  `SetOptions` signer/weight/remove-signer operations against the
  wallet's account - so shared-access approvers, unlike sub-wallet
  creation's primary signer, genuinely do become real Stellar multisig
  signers with real weight, and the threshold really is
  `NumberOfApprovalsNeeded`. This part of the original **is** real
  on-chain multisig.
- Pending multi-approval work is tracked in `PendingAuth` (one row per
  in-flight action: `TransactionType`, `ApprovalsNeeded`,
  `ApprovalsGotten`, `TransactionStatus` PENDING→COMPLETED/REJECTED,
  `TransactionXdr`) and `PendingTransactionSignature` (one row per
  authorizer's submitted co-signature, unique per `(PendingAuthID,
  Approver)` to block double-approval). Each `POST /v1/shared-access/
  approval/:ID` call appends a signature, increments `ApprovalsGotten`,
  and - only once `ApprovalsGotten >= ApprovalsNeeded` - the *same*
  request that supplied the last needed signature triggers
  `SubmitApprovalsXdrWithSignaturesReturnsTrx` in one step. `TransactionType`
  spans `PAYMENT`, `SWAP`, `MODIFY SHARED ACCESS`, `DISABLE SHARED
  ACCESS`, `MAKE MARKET OFFER`, `CRYPTO WITHDRAWAL`, `TOKENIZE ASSET`,
  `ASSET SUBSCRIPTION`, `TOKENIZED ASSET EARLY EXIT` - each with its own
  post-submission processing block (fee/VAT rows, webhook callbacks,
  domain-record creation) keyed off that string.
- Modifying or disabling shared access, once approvers already exist,
  itself goes through this same pending-approval mechanism (`MODIFY
  SHARED ACCESS`/`DISABLE SHARED ACCESS` transaction types) rather than
  applying immediately - changing the signer set is itself a
  signer-set-guarded operation.
- Routes live under `/v1/shared-access/...` (confirmed: `.../users/
  account` for grant/modify/disable, `.../approval(s)` for the approval
  queue, plus per-domain initiator routes like `.../payment`,
  `.../swap`, `.../crypto/withdrawals`); all guarded by the same
  universal signature middleware, with VIEW-ONLY/INITIATOR/APPROVER
  checked by hand inside each handler against `WalletsSharedWithUser`,
  not by a role-aware middleware layer.
- **No curated-asset filtering exists on the balance endpoint** in the
  original - `getSharedAccessWalletBalancesHandler` returns every Horizon
  balance line, native and every trustline, unfiltered; the curated-asset
  table is only used elsewhere for pricing/image lookups on a swap
  picklist. The filtered-to-curated-assets balance view described in this
  redesign's brief is a genuine improvement over the original's actual
  behavior, not a parity requirement - worth building as asked, just
  flagged here as a deliberate departure rather than a faithful port.

### 13.2 The problem: Base has no native weighted-signer account

A Stellar account is natively a multisig primitive - any address can
carry N signers with weights and a threshold, changeable by a signed
`SetOptions` operation. A Base EOA has none of this: it is controlled by
exactly one private key, full stop. Replicating "an address whose signer
set and threshold can be configured and changed" on Base requires that
address to *be a smart contract*, not an EOA.

### 13.3 Decision: Safe smart accounts as the one primitive for both features

**Recommendation, corrected from an earlier draft of this section**:
**every wallet - including the primary, not just sub-wallets** - is
deployed as a [Safe](https://safe.global) smart account (the audited,
canonical M-of-N smart contract wallet, deployed on Base mainnet and
Sepolia at well-known cross-chain addresses via its own CREATE2 factory).
An earlier pass at this section treated the primary wallet as a bare EOA
with no contract involved, which works fine in isolation but breaks once
wallet recovery (§15) is accounted for: recovery only makes sense if the
wallet whose controlling key is being replaced can *have* its controlling
key replaced, and a bare EOA cannot - an EVM address, unlike a Stellar
account, *is* its key. Making the primary wallet a Safe too, with a
separate signer EOA behind it, is what makes recovery possible at all,
and - see below - turns out to simplify everything else in this section
as well, not just enable §15.

This one primitive (every wallet is a Safe) replaces *all three* of the
original's separate mechanisms (Stellar's native account-level signer
weights used for: the primary wallet's own key, the ad hoc "add primary
as signer" sub-wallet trick, and the fuller `SetOptions`-based
shared-access signer management) with a single concept: **every wallet -
primary, sub-wallet, or shared-access group - is an address with an
owner set and a threshold, full stop.** What differs between them is not
their type, only what currently sits in that owner set.

**The load-bearing mechanic: every wallet's owner is the controlling
user's *primary wallet address*, never a raw signer key.** Safe supports
EIP-1271 "contract signatures" - an owner can itself be a smart contract
rather than an EOA, in which case Safe resolves a submitted signature by
calling that contract's own `isValidSignature`. Using this
recursively - a sub-wallet's owner is the primary wallet's Safe address;
a shared-access wallet's member entries are each participant's primary
wallet Safe address - means:
- **The primary wallet's own internal owner (the actual signer EOA) is
  the *only* raw key referenced anywhere in the whole system.** Every
  other wallet a user controls or has been granted access to points at
  their permanent primary-wallet address, and resolves through it.
- **Rotating a user's signer key touches exactly one Safe: their own
  primary wallet.** No sub-wallet, and no shared-access wallet they
  participate in, needs any on-chain update when this happens - the next
  time anything checks "is this a valid signature from that owner," it
  transparently re-resolves through the primary wallet's *current*
  internal owner. This is what makes wallet recovery (§15) collapse from
  the original's "loop over every eligible wallet and individually swap
  its signer" into "swap the signer on one Safe, done" - and it
  simultaneously explains, precisely, why "the user you want to grant
  shared access to must already have their wallet activated on-chain"
  (this section's user-supplied requirement): EIP-1271 resolution calls
  a contract's code, and a counterfactual (undeployed) Safe has none to
  call. §13.11 spells out every activation-order consequence of this.

Why Safe over a bespoke minimal multisig contract: this is a financial
custody product, and Safe is the most heavily audited, most widely
deployed contract of exactly this shape in the entire EVM ecosystem.
Writing a smaller custom contract would shave some gas per execution but
introduces novel signature-validation/replay/reentrancy surface this
project would have to design, implement, and audit itself for a first
time - a bad trade against Base's already-low L2 gas costs. This is a
recommendation, not a foreclosed decision - flag if a custom minimal
contract is wanted instead once implementation starts, but Safe is what
the rest of this section assumes.

**One and only one raw keypair still needs to exist: the signer behind
the primary wallet's Safe.** Registration generates it exactly as
before - a mnemonic-derived EOA - and that EOA becomes the *sole initial
owner* of the primary wallet's own Safe (`owners: [signerEOA], threshold:
1`). This is the direct continuation of the original's `User.PublicKey`
(permanent identity) vs. `User.PrimarySigner` (the currently-operating
key) split, which an earlier draft of this section had incorrectly
collapsed into one and the same address - reinstated here as `User.Address`
(the primary wallet's Safe address, permanent) vs. a new field carrying
the signer EOA (replaceable, exactly what §15's recovery feature
replaces).

**A structural simplification Base's design gives us for every wallet
*other than* the primary, not something we have to engineer**: a Safe's
initial owner set is passed as constructor/initializer data to a
*permissionless* factory call (`createProxyWithNonce`) - there is no
"existing owner" yet at deployment time, so nothing needs to co-sign the
act of setting the initial owners. Concretely, this means:
- **The mobile app never needs to generate a throwaway keypair for a new
  sub-wallet at all.** On Stellar, the new account's address *was* a
  keypair's public key, so one had to exist somewhere, if only to be
  discarded. A Safe's address is a CREATE2 hash of `(factory, singleton,
  initializer calldata, salt nonce)` - it doesn't correspond to any
  keypair, ever. The backend can pick the salt itself.
- **There is no second signature to collect at sub-wallet creation.**
  Where the original needs `PrimarySignature` *and* `SubWalletSignature`
  before submitting, deploying a Safe with `owners: [primaryWalletAddress],
  threshold: 1` needs zero owner signatures - only an authenticated
  *request* from the primary asking for it (via §12's per-request
  signature auth), not a transaction-level co-signature from a wallet
  that doesn't exist yet. Note the owner named here is the **primary
  wallet's Safe address**, not the signer EOA directly - the nested
  EIP-1271 mechanic described above.
- This satisfies "disable the sub-wallet from signing its own
  transactions" more strictly than the original's own audited behavior
  (§13.1) does for ordinary sub-wallets: there is no sub-wallet key to
  disable, because one is never created.
- The address is knowable **before** deployment (a standard "counterfactual"
  Safe) - it can be shown to the user, and can even receive deposits,
  before the contract is actually deployed on-chain. A sub-wallet can
  even be *deployed* naming an as-yet-undeployed primary wallet as its
  owner (the constructor doesn't check that the owner address has code)
  - but it cannot be *operated* (any `execTransaction` against it) until
  the primary wallet is actually deployed, since resolving the nested
  EIP-1271 signature requires code to call. §13.11 covers this and every
  other activation-order consequence precisely.

### 13.4 Schema mapping

| Original | Base equivalent |
|---|---|
| `User.PublicKey` (permanent identity) | `User.Address` (already exists) - the primary wallet's **Safe** CREATE2 address, corrected from an earlier draft's "it's the bare EOA" |
| `User.PrimarySigner` (the currently-operating key) | New field, e.g. `User.SignerAddress` - the EOA that is currently the primary wallet Safe's sole (or, post-recovery-enrollment, one of two) owner. This is the field §15's recovery feature replaces; `User.Address` never changes |
| `UserWallet.ID == Signer` ⇒ primary wallet | Primary wallet: a `ClosedGroup` row whose `Address == User.Address`, `owners: [User.SignerAddress]`, `threshold: 1` - deployed as a Safe like everything else, not a bare EOA |
| `UserWallet.ID` (sub-wallet address, a keypair) | Sub-wallet: `Address` = its own Safe's CREATE2 address (never a keypair) |
| `UserWallet.Signer` | `GroupMember.MemberAddress` for the entry representing "the wallet's owner" - and this is always the owning user's **primary wallet Safe address** (`User.Address`), never their signer EOA directly. Same rule for every `GroupMember` row on any shared-access wallet: it names participants by their permanent primary-wallet identity, resolved through nested EIP-1271, so a participant's own signer rotation (their own recovery event, §15) never requires touching a wallet they merely participate in |
| `SharedAccessEnabled`, `NumberOfApprovalsNeeded` | `ClosedGroup.Disabled` (inverted), `ClosedGroup.Threshold` - **already exist** in the built `sharedaccess` component, reusable as-is |
| `WalletPermission` (grantee, wallet, permission) | `GroupMember` (`GroupID`, `MemberAddress`, `Role`) - **already exists**, `GroupRole` enum already has `INITIATOR`/`APPROVER`/`VIEW_ONLY`. `MemberAddress` semantics corrected per the row above |
| `PendingAuth` | `PendingAction` - **already exists**, needs the gaps in §13.7 closed |
| `PendingTransactionSignature` | `PendingActionApproval` - **already exists** |

**A model-level decision this section proposes**: fold "sub-wallet" and
"shared-access group" into the *same* table rather than keeping them as
the original's two separate concepts (`UserWallet` vs `ClosedGroup`).
**Every** wallet - the primary included - is a `ClosedGroup` row from the
moment it's created. The primary wallet's row has one member: the
signer EOA itself (the one place a raw key, not a nested primary-wallet
reference, is the `MemberAddress` - it has to be, since it's the root of
the whole resolution chain). Every other wallet's row - sub-wallet or
shared-access group - has member(s) that are primary-wallet addresses
(§13.3's nested EIP-1271 mechanic), never raw signer EOAs. A freshly
created sub-wallet is simply a group with one member (the creating
user's primary wallet address, role `INITIATOR`+`APPROVER` combined,
threshold 1); "enabling shared access" is just adding more `GroupMember`
rows (each naming another participant's *primary wallet* address) and
raising `Threshold`, not a different feature bolted onto a different
table. This removes the original's two-different-mechanisms design
entirely rather than porting it twice.

### 13.5 The critical existing gap: `sharedaccess` isn't real multisig today

The already-built `internal/components/sharedaccess` component (Phase
29-33 of this project) does **not** provide on-chain multisig security
today, and this needs fixing as part of this redesign, not carried
forward: `CreateGroup` derives the group's on-chain address via
`cryptoutil.DeriveKey` - a deterministic **EOA private key held and used
server-side**. `tallyAndMaybeExecute`/`executeAction` re-derive that same
key and call `Blockchain.SignAndSubmitTx` once enough `PendingActionApproval`
rows exist. This means the actual security boundary today is "does the
backend's approval-counting logic decide to sign," not "does the
blockchain itself refuse to move funds without N real signatures" - a
compromised or buggy backend can move every group's funds regardless of
how many approvals exist, because the backend holds the one key that
controls everything. This is a materially weaker security model than
the original's real Stellar multisig, and directly contradicts the
premise of "shared access" as a security feature. §13.3's Safe-based
design fixes this: the contract itself enforces the threshold via
`execTransaction`'s own signature verification, independent of whether
the backend that submits the call is honest.

### 13.6 Sub-wallet creation flow on Base

0. **Precondition**: the requesting user's primary wallet must already
   be deployed on-chain - see §13.11. The initial owner named in step 2
   is the primary wallet's *Safe address*, and while the sub-wallet's own
   deployment doesn't strictly require that address to already have code
   (a constructor argument is just a value), the sub-wallet cannot
   actually be *used* until it does, and the funding transfer in step 3
   (if sourced from the primary's own balance, §13.7) cannot execute at
   all without it.
1. Primary requests sub-wallet creation (authenticated via §12's
   per-request signature, naming a tag/description - no public key to
   submit, since none needs generating).
2. Backend picks a salt (e.g. derived from `userID + tag`), computes the
   Safe's CREATE2 address off-chain (deterministic, no chain call
   needed) with `owners: [primaryWalletAddress]` (the primary's Safe
   address, not its signer EOA), and returns it immediately - this can
   already be shown to the user and can receive funds.
3. Deployment (via the canonical `SafeProxyFactory.createProxyWithNonce`,
   pointed at Base's already-deployed `SafeL2` singleton - no need to
   deploy our own) happens either right away or lazily on first outgoing
   transaction; either way it needs no owner signature, only a submitted
   transaction from *some* funded address (see §13.7 for who pays gas).
4. On confirmed deployment, insert the `ClosedGroup` row (`Address` =
   the Safe address, `Threshold: 1`) and a `GroupMember` row naming the
   primary wallet's address, role covering both initiate and approve at
   threshold 1.
5. Wallet lookup "by user ID" is a `ClosedGroup`/`GroupMember` join on
   `MemberAddress`; "by signer" (in the sense of "wallets this user's
   *current* signer key can ultimately operate") resolves through
   `User.Address` first, then the same join - both straightforward once
   §13.4's unified table is in place (the original's `db/user_go.go`
   `id/temp_public_key/signer` OR-query collapses to one join).

### 13.7 Economical activation - gas options

Base's L2 fees are already low (typically well under a cent per
transaction), so "economical" here is about avoiding *unnecessary*
overhead on top of that floor, and about who fronts it:

| Option | How it works | Tradeoff |
|---|---|---|
| **A - Safe's built-in gas refund (recommended to start)** | `execTransaction`'s own `gasPrice`/`gasToken`/`refundReceiver` parameters let the Safe reimburse whoever submits the call, paid from the Safe's *own* ETH balance. A small ETH top-up at creation time (directly analogous to the original's DB-configurable `ActivationAmount`) funds this float; a backend-operated relayer submits transactions and gets reimbursed by each Safe it acts for. | Needs a relayer service - specifically a **pool** of relayer EOAs, not one (§13.12 - the direct Base equivalent of the original's channel-account pool, needed for the same reason: many concurrent submissions from a single account contend for that one account's nonce) - and each wallet needs a small ETH float, refilled periodically. Closely mirrors the original's "activates using the primary wallet's balance" framing, since the top-up itself is typically funded by a transfer from the primary at creation time. |
| **B - Primary EOA pays gas directly** | Primary wallet holds a small ETH balance and submits `execTransaction` itself as an ordinary sender. | Simplest to build, but pushes a "keep some ETH around" UX requirement onto the user - the exact thing Stellar's model doesn't require of end users, since the original abstracts even the *native-asset* activation amount away as an internal bookkeeping detail. |
| **C - ERC-4337 (account abstraction) + Paymaster** | A `UserOperation` flow where a project-funded paymaster sponsors gas entirely - zero ETH ever required from the user for this. | Real engineering lift (bundler integration, paymaster contract/service) beyond what this feature needs on day one; worth keeping as a documented future upgrade path once volume justifies it, not a blocker now. |

**Recommendation**: start with Option A - it reuses the existing
`ActivationAmount`-style config pattern almost verbatim (a small,
per-wallet-type configurable ETH top-up) and keeps gas UX fully backend-
managed, matching the original's "user never thinks about the reserve
requirement" experience. Document C as the natural next step if a fully
gasless product experience becomes a priority later.

### 13.8 Shared-access parity: closing the gaps in what's already built

Per the existing-implementation audit, the built `sharedaccess` component
already gets right: the three-role model, threshold-derived-from-
membership (not a separate stored count, matching the original), the
proposal→approval→tally→execute pipeline, and duplicate-approval
protection. What needs adding for full parity (beyond the §13.5 Safe
migration itself):

- **Member management** - the original's `ModifySharedWalletAccess`/
  `CreateSharedWalletAccess` let an owner add/remove/re-role members and
  change the threshold after the fact, itself gated through the same
  pending-approval mechanism once approvers already exist (a `MODIFY
  SHARED ACCESS` pending action, mapped here to a Safe `addOwnerWithThreshold`/
  `removeOwner`/`swapOwner`/`changeThreshold` call proposed and approved
  exactly like any other action). Today's `sharedaccess` has no such
  operation at all - membership is fixed at `CreateGroup` time.
- **Group disable/revoke** - the original's `DELETE /v1/shared-access/
  users/account` (a `DISABLE SHARED ACCESS` pending action, blocked while
  another shared-access change is already pending) has no equivalent
  today.
- **Generic per-domain post-processing hook** - the original's
  `TransactionType` switch creates a domain record (fee row, withdrawal
  request, market offer, tokenization record, etc.) after each
  successful execution. The built `PendingAction.Kind` today only
  distinguishes `payment`/`swap`/`contract_call` - since a Safe
  transaction is inherently generic (`to`/`value`/`data`), the cleanest
  port is to keep this infrastructure domain-agnostic and let each
  business component (crypto withdrawals, tokenization, market-making)
  create its own `PendingAction` referencing its own record and register
  a callback invoked on execution - this needs a small mechanism (an
  `OnExecuted` hook or a `RelatedRecordID`+`Domain` pair the caller reads
  back), which does not exist yet and is worth designing at
  implementation time rather than fully speccing every domain's
  post-processing here.
- **Cross-wallet listing** - "all wallets belonging to a user, both
  owned and shared to them" (the original's `GetAllWallets` +
  `WalletsSharedWithUser` reverse association) has no equivalent
  aggregator in the port today; add one query joining owned wallets with
  `GroupMember` rows keyed by the caller's address.
- **Curated-asset-filtered balance summary** - genuinely new relative to
  the original (§13.1's finding), but a reasonable ask on its own terms:
  a per-wallet balance endpoint that iterates the existing curated-token
  catalog (`assets.CuratedToken`/`ListCurated()`, already built) and
  returns only those balances, ignoring anything not on the curated list.
- **Naming**: purely cosmetic, but worth a decision before implementation
  - keep `APPROVER` (already in code) or rename to `AUTHORIZER` to match
  the user's own vocabulary for this feature. No functional impact
  either way.

**A consequence flagged here, not fully designed here**: the already-built
`payments` and `swaps` components (early phases of this project) build
and submit ordinary EIP-1559 transactions, under the assumption - correct
at the time - that the primary wallet is a bare EOA. Once the primary
wallet is a Safe (§13.3), every payment and swap sourced from it (and
from any sub-wallet) needs to become a Safe transaction instead: `/build`
returns Safe-transaction fields plus EIP-712 typed data instead of a raw
`network.UnsignedTx`; `/submit` accepts a signature over `SafeTxHash`
and relays it as `execTransaction`, gas-sponsored per §13.7, rather than
broadcasting an already-fully-signed raw transaction. This touches both
`payments` and `swaps` end to end and is real, non-trivial follow-up
work - out of scope for this section to fully redesign, but explicitly
called out so it isn't discovered as a surprise once §13's core work is
underway. `wallet-web/PLAN.md` §6.3 already reflects the client-side
half of this change.

### 13.9 Open decisions needing a call before implementation

- Safe vs. a custom minimal multisig contract (§13.3 recommends Safe).
- Gas-sponsorship model for deployment/execution (§13.7 recommends
  Option A to start).
- `APPROVER` vs `AUTHORIZER` naming (§13.8, cosmetic).
- Whether the unified sub-wallet/group table (§13.4) fully replaces
  `UserWallet` for non-primary wallets, or whether `UserWallet` stays as
  a thin index row pointing at a `ClosedGroup` - an implementation-time
  call once the exact query patterns other components rely on
  (`UserWallet` is referenced well beyond `sharedaccess` - payments,
  assets, swaps) are inventoried.
- Scope and timing of the `payments`/`swaps` Safe-transaction rework
  flagged above - a follow-up design pass of its own, not detailed in
  this document.

### 13.10 Phased roadmap (done - all 9 phases; see each phase's own "implementation notes" subsection below for what shipped)

| Phase | Scope |
|---|---|
| 1 | Safe factory/singleton wiring on Base (address constants, CREATE2 address computation helper, deployment call), EIP-1271 nested-signature construction/verification helper |
| 2 | Primary wallet becomes a Safe: `User.SignerAddress` field, primary-wallet Safe deployment flow (§13.11), migrating registration off "primary is a bare EOA" |
| 3 | Unified sub-wallet/group schema migration (§13.4), sub-wallet creation flow (§13.6) replacing `POST /v1/users/subwallet`'s two-phase XDR dance, owned by the primary wallet's Safe address |
| 4 | Gas-refund float wiring (§13.7 Option A) and relayer submission path, built on the relayer-EOA pool + startup reconciliation from §13.12 (not a single relayer) |
| 5 | Member management + group disable (§13.8), routed through the existing propose/approve/execute pipeline, `GroupMember.MemberAddress` always a participant's primary-wallet address (§13.4) with an activation precondition check (§13.11) |
| 6 | Per-Safe nonce reservation for `PendingAction` proposals (§13.12 risk 1) and the stale-`PendingAction` expiry sweep |
| 7 | Generic per-domain post-processing hook (§13.8); wire crypto/tokenization/market-making initiators onto it |
| 8 | Cross-wallet listing + curated-asset-filtered balance summary (§13.8) |
| 9 | Full build/vet/test/tidy pass; a live smoke test deploying a primary wallet, creating a sub-wallet, enabling shared access, and driving one action through propose→approve→execute on Base Sepolia, including a concurrent-proposal test exercising §13.12's nonce reservation |

#### Phase 1 implementation notes (done)

New `internal/safe` package (no dependency on any other component - pure
on-chain-primitive helpers, unit-testable without a running chain or
database):

- **Canonical v1.4.1 addresses** (`ProxyFactoryAddress`, `SingletonAddress`
  = SafeL2, `CompatibilityFallbackHandlerAddress`) - fetched from
  `safe-global/safe-deployments`' published deployment JSON and confirmed
  identical on Base mainnet (8453) and Base Sepolia (84532), since Safe
  ships these via a chain-agnostic deterministic deployer rather than a
  per-chain deployment. SafeL2 (not plain Safe) is the singleton
  deliberately: it emits per-transaction events a tracing-node-free
  indexer (this codebase's payment-history-engine, and the existing
  chain-log polling worker) needs to see multisig activity at all.
- **`ComputeProxyAddress`** - reproduces `SafeProxyFactory.
  createProxyWithNonce`'s CREATE2 formula exactly (salt =
  `keccak256(keccak256(initializer) || saltNonce)`, init code = the
  `SafeProxy` v1.4.1 creation bytecode - fetched from the
  `@safe-global/safe-contracts@1.4.1` npm package's compiled artifacts,
  not hand-derived - with the singleton address appended), via
  `crypto.CreateAddress2`. This is what lets PLAN.md §13.11's
  activation-order rules be enforced (a wallet's address is known and can
  be referenced/funded before it's deployed).
- **`EncodeSetupCalldata`/`EncodeCreateProxyWithNonceCalldata`/
  `EncodeExecTransactionCalldata`** - ABI encoders for the three Safe/
  SafeProxyFactory methods this codebase needs, via a small embedded ABI
  (same pattern as `internal/network`'s `erc20ABIJSON`) rather than
  routing through the generic JSON-arg `network.EncodeContractCall`, since
  every caller already has concrete Go types. Every Safe this codebase
  deploys is configured with no delegatecall, no setup-time payment, and
  `CompatibilityFallbackHandlerAddress` installed as its fallback handler
  (required for EIP-1271 to work via `isValidSignature` at all); every
  `execTransaction` call always zeroes `safeTxGas`/`baseGas`/`gasPrice`/
  `gasToken`/`refundReceiver` - the relayer pool (Phase 4, §13.12) pays
  execution gas directly as tx sender rather than asking the Safe to
  refund it in-band.
- **EIP-712 hashing** (`DomainSeparator`, `SafeTxHash`,
  `EncodeTransactionData`) and the **nested-message wrapping**
  (`EncodeMessageDataForSafe`/`MessageHashForSafe`, reproducing
  `CompatibilityFallbackHandler.encodeMessageDataForSafe`) that §13.3's
  nested-ownership design needs: when a Safe is itself an owner of
  another Safe, the outer Safe's `checkNSignatures` resolves that owner's
  signature via EIP-1271, which - through the fallback handler - re-wraps
  the outer transaction's pre-image bytes as a `SafeMessage` under the
  *nested* Safe's own domain separator, recursing into the nested Safe's
  own owner set. Getting this wrapping wrong would silently break
  approval verification for exactly the shared-access-across-wallets case
  this whole redesign exists to support, so all three EIP-712 typehash
  constants (`DOMAIN_SEPARATOR_TYPEHASH`, `SAFE_TX_TYPEHASH`,
  `SAFE_MSG_TYPEHASH`) are cross-checked in tests against a from-scratch
  `keccak256` of their Solidity signature strings, not just copied and
  trusted.
- **Signature packing** (`PackSignatures`, `EOAPersonalSignSignature`,
  `ContractSignature`) - assembles the exact packed-signature blob
  `execTransaction`/`checkNSignatures` expects: a 65-byte-per-owner static
  array sorted by strictly-ascending owner address (`checkNSignatures`
  rejects any other order), EOA signatures using the "personal_sign"
  `v+4` encoding (`v` rewritten to 31/32) so that Safe transaction
  approvals reuse the same `personal_sign` primitive PLAN.md §12
  standardized every other approval flow on, and contract signatures
  (`v=0`, `r`=owner address, `s`=offset into a length-prefixed dynamic
  tail) for the nested-Safe-as-owner case.
- **`cryptoutil.VerifyPersonalSignBytes`** added alongside the existing
  `VerifyPersonalSign` - verifies a `personal_sign` signature of a raw
  byte digest (e.g. a `SafeTxHash`) rather than a UTF-8 string, needed
  because a transaction hash is binary data, not text.
- **Verification**: every typehash, the CREATE2 address computation, the
  ABI-encoded `setup` calldata, the domain separator, and the final
  `SafeTxHash` are all checked in `safe_test.go` against fixed test
  vectors computed by an independent from-scratch Python
  re-implementation of the same formulas (`eth_abi` + `pycryptodome`,
  not this package or go-ethereum) - deliberately not just testing that
  the Go code agrees with itself. Signature-packing tests cover sort
  order, the `v+4` rewrite, duplicate-owner rejection, and the
  contract-signature dynamic-tail layout. No network/database
  dependency - full local `go test` coverage.
- Not yet done at the time this was written (now done - see Phase 2's
  notes just below): deploying a Safe and updating the `users` component
  to compute and store one. Still outstanding for later phases:
  submitting `execTransaction` and collecting real owner signatures into
  a `PendingAction`-style flow (Phases 3-6).

#### Phase 2 implementation notes (done)

- **`User.SignerAddress`** (new, unique, not-null) is the EOA that proved
  ownership via `middleware.SignatureAuth` at registration - the raw key
  a client actually signs with. **`User.Address`** keeps its existing
  column but changes meaning: it's now that signer's primary wallet Safe
  address (`safe.ComputeProxyAddress(safe.SingletonAddress,
  safe.EncodeSetupCalldata([signer], threshold=1), saltNonce=0)`),
  computed once in `Register` and never changed except by wallet recovery
  Branch B (§15.6, not yet built). Reusing saltNonce 0 for every user is
  safe: Safe folds `keccak256(initializer)` into the actual CREATE2 salt,
  and each user's initializer already differs because it names a
  different sole owner.
- **`User.PrimaryWalletDeployed`** (new, default false) tracks whether
  that Safe has actually been deployed on-chain yet - always false right
  after registration (PLAN.md §13.11: registration never requires
  activation).
- **`Service.DeployPrimaryWallet(ctx, signerAddress)`** (new) submits the
  `SafeProxyFactory.createProxyWithNonce` call that deploys the caller's
  own primary wallet at the address `Register` already computed - a
  permissionless factory call, so it's paid for by a single
  platform-operated key (`deriveDeployerKey`, seeded by the new
  `PRIMARY_WALLET_DEPLOYER_KEY_SALT`, same derived-key pattern as every
  other server-controlled role in this codebase) rather than needing any
  authority over the wallet itself. Idempotent - a no-op success if
  `PrimaryWalletDeployed` is already true - so a client (or a future
  activation-flow hook) can call it freely. Exposed at
  `POST /v1/users/wallet/deploy`, self-service only.
  Wiring this automatically into `fiat`'s existing paid-activation flow
  (`ProcessActivation`, which already dispenses starter gas to
  `user.Address`) is a natural follow-up - deliberately not done in this
  phase to keep it decoupled and independently testable; nothing about
  deployment being manual/self-service blocks a later automatic trigger
  from calling the same idempotent method.
- **`middleware.SignatureAuth` self-service path rewritten**: since a
  wallet's own address is now a Safe rather than the signer's EOA, the
  ordinary case has `signer != wallet` even for a self-service call. A
  new `signerIsPrimaryWalletOwner` lookup (`users.User` where
  `Address = wallet AND SignerAddress = signer`) is checked before
  falling through to the existing sharedaccess standing check; the
  original `signer == wallet` fast path is kept too (not removed) since
  it's what account-recovery Branch A's bare, undeployed post-recovery
  identity relies on (see next bullet). This is the one change in this
  phase with system-wide blast radius - every existing protected route
  keeps working unchanged (they all just read `CtxSubject`), but every
  *client* must now send the computed Safe address as
  `X-Wallet-Address`, not the signer's own address, for every call after
  registration.
- **Registration flow**: `POST /v1/users` still signs
  `signer == wallet` (there's no wallet to name yet) - the controller now
  reads `CtxSigner` explicitly rather than `CtxSubject` to make clear
  it's registering a raw EOA, not a wallet address.
  `RegisterInput.Address` was renamed to `SignerAddress` throughout
  (including servicelinks' `OnboardUser`, which onboards a partner's
  already-provisioned EOA the same way).
- **Account-recovery Branch A** (`recovery.go`, unchanged in behavior)
  now also sets `SignerAddress = newAddress` alongside `Address`, not
  just `Address` - otherwise `SignerAddress` would keep pointing at the
  lost key forever, breaking `signerIsPrimaryWalletOwner` for exactly the
  account that just recovered. This matches Branch A's own documented
  intent (PLAN.md §15's table: "a fresh identity, nothing preserved") -
  the recovered account becomes a bare, undeployed EOA-as-wallet identity
  (signer == wallet == newAddress), exactly like every account looked
  before this phase, not a fresh Safe.
- **Verification**: `internal/components/users/services` tests cover
  `Register` computing the correct Safe address (distinct from the
  signer), `DeployPrimaryWallet`'s idempotency and failure propagation,
  and existing recovery/security-answer flows continuing to pass
  unchanged. `internal/middleware` tests cover the new
  `signerIsPrimaryWalletOwner` path alongside the pre-existing
  self-signing and shared-access-standing cases. Full `go build`/`go
  vet`/`go test ./...` pass across the module.

#### Phase 3 implementation notes (done)

- **`models.RoleInitiatorApprover`** (new `GroupRole` value) plus exported
  `CanInitiate(role)`/`CanApprove(role)` predicates replace direct
  `role != models.RoleInitiator`/`RoleApprover` comparisons throughout
  `sharedaccess.Service`. Needed because `GroupMember` allows only one row
  per `(group, address)` pair, yet a sub-wallet's sole owner (§13.4: "a
  sub-wallet is simply the single-member case") must be able to both
  propose and approve in that one row - a combined role was the only way
  to unify sub-wallets and multi-member shared-access groups onto the
  same schema and the same `CreateGroup` without a separate code path for
  the single-owner case.
- **`CreateGroup` now deploys a real Safe**, closing half of §13.5's
  flagged gap. It takes a `ctx` (new first parameter), builds the
  `owners` list from every member with `CanApprove(role)`, validates each
  candidate address via the new `validateSafeOwnerCandidate`, computes
  the group's counterfactual address the same way §13.3/Phase 1 compute a
  primary wallet's (`safe.EncodeSetupCalldata` + `safe.
  ComputeProxyAddress`), then actually submits `createProxyWithNonce`
  through the shared deployer key and only persists the `ClosedGroup`/
  `GroupMember` rows once that submission succeeds. Unlike a primary
  wallet, the salt nonce can't be a fixed `0`: two different groups can
  trivially share an identical owner set and threshold (e.g. one user
  creating two single-owner sub-wallets back to back), which would
  collide on the same CREATE2 address - so `groupSafeSaltNonce` draws a
  random 256-bit value via `crypto/rand` per group instead.
- **`validateSafeOwnerCandidate`** rejects two ways a caller could name an
  owner that would leave the group permanently or confusingly broken: (a)
  a registered user's raw `SignerAddress` given as the owner (must be
  their primary wallet's `Address` instead - naming the EOA directly
  would make EIP-1271 resolution look at the wrong contract), and (b) a
  registered user's primary wallet named as owner while
  `PrimaryWalletDeployed` is still false (per §13.11, `isValidSignature`
  has no code to call on an undeployed Safe - the group would be
  unusable via that owner until they separately deploy it). Any other
  address - an external EOA, an address with no matching `users` row, or
  an already-deployed primary wallet - is accepted as-is.
- **`GroupKeySalt` retired, folded into `SafeDeployerKeySalt`**: deploying
  a Safe via its factory grants the deployer no ongoing authority over
  the resulting wallet, so the same platform key Phase 2 introduced for
  primary-wallet deployment now also pays for group deployment
  (`deriveSafeDeployerKey`, `salt + "|safe-deployer"` - a different
  derivation string than users' `|primary-wallet-deployer"`, same salt
  value, same funded address in practice). `GroupKeySalt`/
  `GROUP_KEY_SALT` removed entirely from `sharedconfig`, `main.go`,
  `.env.example`, and `DEPLOYMENT.md`.
- **`executeAction` deliberately fails loudly instead of executing**: the
  old body derived a custodial `groupKey` from `GroupKeySalt` and
  submitted the pending action's raw calldata directly - a mechanism with
  no relationship at all to the Safe this phase now actually deploys, and
  the other half of §13.5's flagged gap (the backend, not the chain,
  enforced the threshold). Rather than either leave that stale path
  running against an address it doesn't control, or build out real
  `execTransaction` submission with collected `SafeTxHash` signatures
  before the relayer/nonce-reservation machinery those steps depend on
  exists, `executeAction` now unconditionally returns
  `apperrors.Internal("... on-chain execution via a real Safe transaction
  is not implemented yet - see PLAN.md §13.10 Phases 4 and 6")` once a
  proposal's threshold is met. `ApproveAction`'s off-chain signature
  verification still checks a signature over the existing descriptive
  `canonicalActionMessage` string rather than a real `SafeTxHash` -
  switching that to the actual Safe transaction hash is Phase 4's job,
  once there's a real `execTransaction` call for that hash to describe.
- **Verification**: `sharedaccess/services` tests rewritten around a
  `fakeBlockchain` that records submitted `(to, data)` pairs instead of
  just returning a canned hash - `TestCreateGroup_DeploysARealSafe`
  asserts the submission target is `safe.ProxyFactoryAddress` and that
  two groups with identical members/threshold still get different
  addresses (the random salt nonce). New tests cover the combined
  `RoleInitiatorApprover` role, both `validateSafeOwnerCandidate`
  rejection cases, and accepting an already-deployed primary wallet as a
  member. The propose/approve/execute tests were renamed and rewritten to
  assert the new, honest outcome - tallying and off-chain signature
  verification still work, but reaching threshold now surfaces the
  not-implemented error instead of a fake success, including on a retried
  approval (no second signature required, same error both times). Full
  `go build`/`go vet`/`go test ./...` pass across the module.

#### Phase 4 implementation notes (done)

- **Real execution, finally**: `executeAction` no longer fails loudly - it
  packs every recorded off-chain approval into the real
  `Safe.execTransaction` signatures blob (`safe.PackSignatures`), submits
  it through a pool relayer, and waits for on-chain confirmation before
  marking the action `EXECUTED`. This closes the second half of §13.5's
  flagged gap (the first half, real Safe *deployment*, was Phase 3's).
- **Approvals now sign the real digest, not a descriptive string**:
  `ApproveAction`'s signature check moved from `canonicalActionMessage`
  (an off-chain-only text string) to the actual `safe.SafeTxHash` - or,
  for a member who is a registered user's primary wallet, the nested
  EIP-1271 `MessageHashForSafe` digest that primary wallet's own owner
  (its current `SignerAddress`) must sign instead
  (`resolveGroupOwnerSigner`/`digestToSign`). `GET .../actions/:actionId`
  now returns `digestToSign` (renamed from `messageToSign`, since it's a
  binary hash now, not a message) computed per the calling member -
  `CanonicalActionMessage` is gone.
- **Nested EIP-1271 signature packing** (`buildPackedSignature`): a direct
  external-EOA owner's approval packs as a plain EOA signature; a
  registered user's primary-wallet owner's approval - signed by that
  user's current signer key over the nested digest - packs as an EIP-1271
  contract signature (`safe.ContractSignature`, wrapping an inner
  `safe.PackSignatures` blob over that primary wallet's own single
  owner). This is the first place PLAN.md §13.4's nested-ownership design
  (built and cross-checked in Phase 1, unused until now) actually runs
  end to end.
- **`PendingAction.SafeNonce`** (new column) fixes, at proposal time, the
  Safe nonce every approver's signature is computed against - read with a
  plain `Safe.nonce()` eth_call (`safe.EncodeNonceCalldata`/
  `network.Client.SafeNonce`), not reserved atomically. This is a known,
  deliberately deferred race (two actions proposed concurrently against
  the same Safe can collide) - closing it with a row-locked atomic
  reservation is Phase 6's job (§13.12 risk 1), not this one.
- **`internal/relayer`** (new package): the direct Base equivalent of the
  original's channel-account pool (§13.12 risk 2) - a fixed set of
  relayer EOAs, each deterministically derived from `RELAYER_KEY_SALT`
  (never stored, same convention as every other server-controlled key),
  claimed for the lifetime of one submission and released only once it's
  confirmed on-chain (or fails/times out) - never merely on broadcast,
  the exact bug §13.12's audit found in the original's own shared-access
  code. `PendingAction.RelayerAddress` plus a new transient `SUBMITTED`
  status track which relayer is mid-flight for which action.
- **`Service.ReconcileRelayers`** (called once at boot, before the router
  serves traffic) is the startup-reconciliation counterpart the original
  has for its own pool: it re-marks any relayer address recorded against
  a still-`SUBMITTED` action in-use before the freshly-constructed
  in-memory pool ever serves a `Claim`, then resumes waiting on each - the
  in-memory pool has no memory of its own across a restart, so without
  this a relayer whose last submission's outcome is unknown could be
  handed out for new work immediately.
- **Gas-sponsorship model note**: §13.7's "Option A" (a Safe's own
  built-in `execTransaction` gas refund, paid from a per-Safe ETH float)
  was superseded by a decision already made in Phase 1:
  `EncodeExecTransactionCalldata` always zeroes `gasPrice`/`gasToken`/
  `refundReceiver`, so the relayer pool pays execution gas directly out
  of its own centrally-funded balance rather than being reimbursed
  per-transaction by each Safe. Simpler to implement and reason about
  than in-band refund accounting, and a reasonable simplification given
  Base's already-negligible L2 fees - no per-Safe ETH top-up is needed
  for execution gas (a group's own balance still needs funding for
  whatever it actually pays out, of course).
- **Verification**: `sharedaccess/services` tests now assert real
  execution succeeds and reaches `EXECUTED` once threshold is met (not
  just that it's attempted), cover the nested EIP-1271 packing path with
  a registered-user primary-wallet member end to end (asserting the
  nested digest differs from the plain `SafeTxHash`), and cover retrying
  a failed submission without a second signature (the fake blockchain's
  configurable `err` simulates a transient RPC failure, then clears for
  the retry). New `internal/relayer` tests cover deterministic key
  derivation, distinct addresses per pool, Claim/Release round-tripping,
  and `ReserveAtStartup` keeping a reserved address unavailable until
  released. Full `go build`/`go vet`/`go test ./...` pass across the
  module.

#### Phase 5 implementation notes (done)

- **Member management** (§13.8's flagged gap, closed): four new
  `ActionKind`s - `add_member`, `remove_member`, `change_threshold`,
  `disable_group` - proposed and approved through the exact same
  propose/approve/execute pipeline as a payment or contract call, per the
  original design note ("mapped here to a Safe
  addOwnerWithThreshold/removeOwner/swapOwner/changeThreshold call
  proposed and approved exactly like any other action"). `swapOwner`
  itself wasn't needed: a pure role change that doesn't cross the
  CanApprove boundary (e.g. APPROVER to INITIATOR_APPROVER) needs no Safe
  call at all, and one that does cross it is expressed as a remove
  followed by an add - a deliberate scope cut to keep this phase bounded,
  noted here rather than silently dropped.
- **On-chain vs. application-level, decided per proposal**:
  `ProposeAddMember`/`ProposeRemoveMember` check whether the role
  involved can approve (`models.CanApprove`) - if so, the proposal is a
  real `Safe.addOwnerWithThreshold`/`removeOwner` self-call (`To` = the
  group's own Safe address); if not (VIEW_ONLY or a plain INITIATOR),
  it's pure `ClosedGroup`/`GroupMember` bookkeeping with `To` left empty
  and no relayer/execution machinery touched at all.
  `ProposeChangeThreshold` is always a real Safe call;
  `ProposeDisableGroup` (the original's `DELETE /v1/shared-access/
  users/account` equivalent) is always application-level, since a Safe
  has no "disabled" concept of its own - disabling only stops this
  application from proposing further actions against the group
  (`ClosedGroup.Disabled`, already checked by `requireInitiator`).
- **`safe.FindPrevOwner`/`Safe.getOwners()`** (new): `removeOwner`
  requires the previous owner in `OwnerManager`'s singly-linked-list
  storage layout, computed from a live `getOwners()` read
  (`network.Client.SafeOwners`) rather than assumed - the Safe's actual
  on-chain owner order is the only source of truth for it.
  `ProposeRemoveMember` also re-derives the post-removal approver count
  from that same live read (not the local `GroupMember` table) before
  accepting a caller-supplied `newThreshold`, so the local mirror being
  briefly stale can't let through a threshold the Safe would itself
  reject.
- **A digest for actions with no real Safe transaction**: signing the
  actual `SafeTxHash` (Phase 4) has no equivalent for an
  application-level-only action, since there's nothing on-chain to hash -
  `canonicalManagementDigest` (a deterministic `keccak256` over the
  action's id/group/kind/target/role/threshold) fills that gap, wrapped
  through the same nested-EIP-1271-or-direct logic (`digestToSign`) as a
  real `SafeTxHash` would be, so a primary-wallet member still signs with
  their own current signer key regardless of which of the two cases
  applies to the action itself.
- **`applyMembershipSideEffect`**: once a Safe-calling management action
  is confirmed on-chain (or immediately, for the two application-level
  kinds), this mirrors the effect into `GroupMember`/`ClosedGroup` inside
  one transaction - inserting or deleting the target's `GroupMember` row
  and updating `Threshold`. A failure here after real on-chain success is
  surfaced as a loud error (this application's own bookkeeping drifting
  from the Safe's real owner set is exactly the kind of silent
  inconsistency this whole redesign exists to avoid), not swallowed.
- **Conflict prevention**: `requireNoConflictingManagementAction` blocks
  proposing any of the four management kinds while another one is still
  `PENDING`/`SUBMITTED` on the same group - the direct generalization of
  the original's "blocked while another shared-access change is already
  pending" rule for `DISABLE SHARED ACCESS`, extended to every management
  kind rather than just that one, since two concurrent owner-set changes
  racing against the same Safe is exactly the kind of conflict PLAN.md
  §13.12 already flags elsewhere.
- **Verification**: new tests cover an approving member's addition/removal
  actually calling the Safe (asserting the submission target and that the
  local `GroupMember` row changes only after confirmation), a
  non-approving member's addition/removal never submitting anything
  on-chain, threshold changes both succeeding and being rejected when
  they'd exceed the current approver count, disabling a group blocking
  every further proposal (payments included), and the conflict-prevention
  rule itself. `internal/safe` tests cover the three new self-call
  encoders round-tripping through their own ABI and `FindPrevOwner`'s
  first/middle/last/not-found cases. Full `go build`/`go vet`/
  `go test ./...` pass across the module.

#### Phase 6 implementation notes (done)

- **Per-Safe nonce reservation, but not the literal count-based scheme
  §13.12 sketched**: that section's own wording - "compute nextNonce =
  safe.OnChainNonce + count(non-terminal PendingActions)" - turns out not
  to be correct as written once rejections are considered. A Safe's own
  nonce only increments on a *successful* `execTransaction` call, never
  on this application's own `REJECTED` status, which has no on-chain
  effect at all. So if an earlier-reserved action is rejected before
  executing, its nonce slot is never actually consumed on the real
  Safe - and a later action holding the next nonce up would then revert
  forever once someone tried to execute it, with no recovery short of
  renumbering every other outstanding action (which would invalidate
  every signature already collected for them, since a SafeTxHash
  depends on its nonce). Rather than build that renumbering machinery,
  `reserveSafeNonce` enforces the simpler, provably-correct invariant a
  Safe's own nonce sequence already implies on its own: **at most one
  nonce-consuming action may be PENDING/SUBMITTED per group at a
  time**. This isn't a real capacity loss - a Safe could never actually
  execute two pending proposals concurrently anyway, since its nonce is
  strictly sequential - it just makes that existing constraint explicit
  and enforced instead of letting the database drift into a state the
  chain could never honor.
- **The lock, concretely**: `reserveSafeNonce` runs inside the same DB
  transaction `createPendingAction` uses to insert the new action -
  `SELECT ... FOR UPDATE` (via GORM's `clause.Locking`) on the
  `ClosedGroup` row, then a check for any existing nonce-consuming
  (`"to" != ''`) `PENDING`/`SUBMITTED` action on that group. A second
  concurrent proposal against the same group blocks on the row lock
  until the first transaction commits, then correctly observes the
  first's now-committed row and is rejected with a 409 - never racing to
  both read "none outstanding" and insert conflicting nonces. Once an
  action reaches a terminal state (`EXECUTED` or `REJECTED`), it's
  automatically excluded from that check by the next proposal - no
  separate "release" step needed, closing the original's own analogous
  gap where `RejectTransaction` never released its channel account at
  all.
- **`ExpireStalePendingActions`** (new): a straight port of the pattern
  behind the original's `ExpireStalePaymentInvoices`, applied to the one
  pending-approval flow that was flagged as missing it entirely
  (§13.1's audit) - a periodic sweep (`PENDING_ACTION_TTL_MINUTES`,
  default 24h, checked every 30 minutes from `main.go`, the original's
  own cadence) rejects any `PENDING` action older than the configured
  TTL. Only `PENDING` is in scope - a `SUBMITTED` action already has a
  relayer actively waiting on its confirmation, recovered separately by
  `ReconcileRelayers` if the process restarts mid-wait, so it isn't
  "stale" in the sense this sweep exists to catch. Rejecting it (not
  deleting) frees its nonce reservation immediately for the next
  proposal, exactly like a human rejection would.
- **Verification**: new tests cover a second on-chain proposal being
  blocked while the first is outstanding, a second proposal succeeding
  once the first is rejected (confirming the nonce-reservation gate
  actually clears), and `ExpireStalePendingActions` rejecting only a
  backdated action while leaving a fresh one untouched and re-opening the
  group for a new proposal. Full `go build`/`go vet`/`go test ./...` pass
  across the module.

#### Phase 7 implementation notes (done)

- **`models.PendingAction.Domain`/`RelatedRecordID`** (new, both empty for
  an ordinary group-proposed action): the generic tagging pair §13.8
  called for - a business component outside `sharedaccess` (crypto
  withdrawals, tokenization, market-making, ...) sets them when it calls
  `ProposePayment`/`ProposeContractCall` on a group's behalf, and gets
  them back on whatever `PendingAction` its `DomainHook` receives.
  `sharedaccess` itself never reads or interprets either field beyond
  carrying them through - it stays exactly as domain-agnostic as §13.8
  asked for.
- **`Service.DomainHook`/`RegisterDomainHook`**: a `func(*models.
  PendingAction)` registered per domain string, invoked once an action
  tagged with that domain reaches a terminal state - `EXECUTED` (from any
  of the three execution paths: a real Safe call, a DB-only membership
  change, or a group disable) or `REJECTED` (a human rejection or
  `ExpireStalePendingActions`' expiry sweep, indistinguishable to the
  hook - both just see status `REJECTED`). Wired at boot the same way
  this codebase already wires `kyc.OnBVNVerified` and `tokenization.
  CreateFiatInvoice` - a post-construction callback assignment in
  `main.go` - so neither package ever imports the other directly.
  Registering the same domain twice panics at boot rather than silently
  keeping only one hook, since that would hide a real wiring bug.
  `ExpireStalePendingActions` was changed from a single bulk `UPDATE` to
  a per-row loop specifically so its sweep can still invoke each expired
  action's hook individually - an acceptable cost given how infrequent
  and small a real expiry sweep should be.
- **No business component actually registers a hook yet**: §13.8 itself
  scoped this section to building the mechanism, not "fully speccing
  every domain's post-processing" - crypto/tokenization/market-making
  each still build and submit their own transactions directly today, as
  audited in earlier phases. Wiring any of them onto this new mechanism
  (having a withdrawal, mint, or settlement instead route through a
  shared-access group's propose/approve/execute pipeline) is real,
  separate follow-up work for whenever that migration is undertaken, not
  assumed to be a mechanical consequence of this phase existing.
- **Verification**: new tests cover a hook firing exactly once on
  execution with the right domain/related-record values attached, firing
  on both an ordinary rejection and an expiry-driven one, an ordinary
  (non-domain) action never triggering any hook, and a duplicate
  registration panicking. Full `go build`/`go vet`/`go test ./...` pass
  across the module.

#### Phase 8 implementation notes (done)

- **`ListWalletsForMember`** (new): the port's equivalent of the
  original's `GetAllWallets` + `WalletsSharedWithUser` reverse
  association (§13.8) - but unified into one query rather than two,
  since PLAN.md §13.4 already made a sub-wallet just the single-member
  case of the same `ClosedGroup`/`GroupMember` schema a genuinely shared
  wallet uses. Returns the caller's own primary wallet (looked up in
  `users`, `Kind: "primary"`, a synthetic `Role: "OWNER"` since a primary
  wallet has no real `GroupMember` row at all) plus every `ClosedGroup`
  they're a member of, own sub-wallet and wallet shared to them alike -
  indistinguishable in the result by design, matching how the schema
  itself doesn't distinguish them either.
- **`CuratedBalances`** (new, genuinely new relative to the original per
  §13.1's own finding): iterates `assets.Service.ListCurated()` and reads
  each curated token's balance for one group wallet, ignoring anything
  the wallet might hold that isn't on the curated list. `sharedaccess`
  reaches `assets` through a narrow `CuratedTokenLister` interface
  (mirroring `BlockchainClient`'s own narrowing) rather than a concrete
  `*assets.services.Service`, wired via a post-construction field
  assignment (`sharedaccessSvc.Assets = assetsSvc` in `main.go`) - the
  same cross-component pattern this codebase already uses for
  `paymentsSvc.Alerts`/`usersSvc.GeoIP`, so neither package imports the
  other's concrete type.
- **Routes**: `GET /v1/shared-access/wallets` (list) and
  `GET /v1/shared-access/balance/:groupId/curated` (curated summary),
  both gated by the same `middleware.SignatureAuth` every other route in
  this component already requires - `ListWalletsForMember` needs no
  additional membership check (it only ever returns what the caller is
  already entitled to see by construction), while `CuratedBalances`
  reuses the same `memberRole` check `Balance` already has (any member,
  `VIEW_ONLY` included, may check a balance).
- **Verification**: new tests cover a caller with both a primary wallet
  and a group membership seeing both entries with the right `Kind`/
  `Role`, a caller with no registered primary wallet seeing just their
  group memberships, curated balances coming back only for the curated
  catalog's entries, and a non-member being rejected. Full `go build`/
  `go vet`/`go test ./...` pass across the module.

#### Phase 9 implementation notes (done, with one honest caveat)

- **Full verification**: `gofmt -l .` clean, `go build ./...` clean,
  `go vet ./...` clean, `go mod tidy` produced no changes,
  `go test ./... -race` green across every package in the module
  (`internal/safe`, `internal/relayer`, `internal/network`, every
  `sharedaccess` test, and every other component untouched by §13). The
  binary was also built and booted standalone against a local SQLite
  database (`DB_AUTOMIGRATE=true`, every optional integration left
  unconfigured) - `GET /` returned `{"status":"ok"}`, the relayer pool's
  three derived addresses were logged as expected, and every
  `sharedaccess` route from Phases 3-8 (groups, actions, member
  management, wallet listing, curated balances) registered cleanly with
  no boot-time errors.
- **The concurrent-proposal test Phase 9 explicitly called for**:
  `TestProposePayment_ConcurrentProposalsOnlyOneSucceeds` fires 8 real
  goroutines at `ProposePayment` against the same freshly-created group
  simultaneously (not sequential calls dressed up as a concurrency test)
  and asserts exactly one succeeds, the other seven see the 409
  `reserveSafeNonce` conflict, and exactly one `PendingAction` row exists
  afterward - passing repeatedly under `go test -race`. SQLite's
  `:memory:` DSN gives every new connection its own separate database by
  default, which would have made this test pass trivially (and
  meaninglessly) by accident if left unaddressed - `newTestDB` now caps
  the pool at one connection so every goroutine genuinely contends for
  the same `ClosedGroup` row lock instead of each seeing an empty
  database of its own.
- **The honest caveat**: the roadmap's own Phase 9 line also called for
  "a live smoke test... on Base Sepolia" - deploying a primary wallet,
  creating a sub-wallet, enabling shared access, and driving one action
  through propose→approve→execute against the real network. That did
  **not** happen in this environment: there is no funded Base Sepolia
  deployer/relayer key available here, and fabricating a claim of having
  run it would be worse than not running it at all. Everything short of
  live-network execution has been verified as thoroughly as this
  environment allows - the full unit-test suite exercises every
  mechanism (CREATE2 address computation cross-checked against an
  independent Python reimplementation in Phase 1, real signature
  packing and nested EIP-1271 wrapping, real relayer-pool claim/release
  semantics, the row-locked nonce reservation under genuine concurrency)
  against a fake blockchain client rather than mocked-away logic, which
  is the strongest verification available without spending real testnet
  ETH and provisioning real keys. Running the actual Base Sepolia
  end-to-end smoke test remains open follow-up work for whoever has
  those credentials.
- **This closes PLAN.md §13** (Phases 1-9 all done): the multisig/
  sub-wallet feature is now a real Gnosis Safe-backed system end to end
  - deployment, execution, member management, group disable, nonce
  safety, domain extensibility, and cross-wallet visibility - built up
  phase by phase from the design-only document this section started as.

### 13.11 Activation-order dependencies (user-flagged, audited against §13.1-§13.8's design)

Registration itself never requires on-chain activation - the primary
wallet's Safe address is computed (CREATE2) and stored the moment a
user registers, exactly like a sub-wallet's counterfactual address
(§13.3). But several *later* operations have a real, unavoidable
ordering requirement, each for a different mechanical reason - worth
listing explicitly rather than leaving as something to discover during
implementation:

| Operation | Requires primary wallet already deployed? | Why |
|---|---|---|
| Register | No | The Safe address is computed, not deployed - counterfactual, same as any sub-wallet |
| Create a sub-wallet (the deploy call itself, §13.6 step 2-3) | No, strictly | The constructor just records an owner address as data - it doesn't check that address has code |
| Fund a new sub-wallet's gas float from the primary's own balance (§13.7 Option A) | **Yes** | Moving value out of a Safe means calling `execTransaction` on it - an undeployed Safe has no code to call |
| Operate (execute anything from) a sub-wallet at all | **Yes** | Resolving the sub-wallet's owner (the primary wallet's address) via EIP-1271 means calling `isValidSignature` on that address - nothing to call if it isn't deployed yet |
| Enable wallet recovery (§15) | **Yes** | Adding the recovery service as a second owner of the primary wallet is itself an `execTransaction` call *on the primary wallet* |
| Be added as a member of someone else's shared-access wallet | **Yes, for the invitee specifically** | The inviter's wallet stores the invitee's *primary wallet address* as a `GroupMember` (§13.4) - resolving that member's signature via EIP-1271 later requires the invitee's Safe to have code. This is precisely the user-stated requirement: "the user you want to add must have their wallet already activated on-chain" |

**Open decision**: should primary-wallet deployment be explicit (a visible
"activate your wallet" onboarding step, matching the original's own
transparent "X will be deducted to activate..." messaging pattern) or
lazy (silently bundled into the first operation that needs it - creating
a funded sub-wallet, enabling recovery, or being invited to shared
access elsewhere)? Recommendation: **explicit**, surfaced once during
onboarding right after registration, gas-sponsored per §13.7's chosen
option - a user shouldn't discover an unexpected extra on-chain step
bundled invisibly into an unrelated action's cost. Lazy deployment as a
fallback (for a user who skipped the explicit step and then tries an
operation that needs it) is still worth supporting, just not as the
primary path.

### 13.12 Concurrency: the Base equivalent of the original's channel-account pool

The user asked specifically that this feature's equivalent be retained,
and audited directly (not assumed): the original solves a Stellar-
specific problem (a strictly incrementing sequence number owned by a
transaction's source account, colliding if two transactions for the same
source are built concurrently) with a pool of dedicated "channel
accounts" used as the source account for transactions with a long
pending-approval window - reserving that window's sequence number away
from contention with the wallet's own ongoing activity. Base has an
analogous, but not identical, nonce-collision problem in **two**
different places, both needing the same architectural pattern (a pool,
claim/release, startup reconciliation) - not one.

**Audited finding: the original's own implementation of this feature has
a real bug, for exactly the flows the user described it as protecting.**
There is no `channel_accounts` DB table - the pool is an in-memory Go
buffered channel of keypairs (`sharedconfig.GlobalConfig.ChannelAccounts`)
populated at startup from env config (`CHANNEL_ACCOUNTS` seed CSV,
topped up to `CHANNEL_ACCOUNT_MIN_COUNT` with fresh random keypairs).
Claiming is a channel receive, releasing is a channel send - and the
shared-access code (`generateModifySharedAccessXdr`,
`generateRemoveSharedAccessXdr`) claims one, builds the XDR, and
**releases it immediately via an unconditional `defer`** - the account
is back in the available pool the instant the pending, awaiting-
approval transaction is written to the DB, not held for the whole
approval-collection window the mechanism exists to protect. Only one
call site anywhere in the codebase (a fiat-purchase flow,
`tokenized_assets.go`) does the correct thing - hold it in
`InUseChannelAccounts` via `StoreInUseChannelAccount`, release only on
completion via `ReleaseInUseChannelAccount`. The startup reconciliation
goroutine the user described (scans `PendingAuth` rows with
`transaction_status = 'PENDING'` and re-marks their channel account
in-use) is real and does exactly what's described - but for the
shared-access flows, it's compensating for a bug that leaves the account
available *during the entire live-process window*, only closing the gap
retroactively on the *next restart*. This is flagged, not reproduced -
the Base design below implements the mechanism the original's own
`StoreInUseChannelAccount` pattern was aiming for, applied consistently,
plus the startup-reconciliation safety net the user asked to keep.

**Two distinct nonce-collision risks on Base, both real:**

1. **A Safe's own internal nonce** (used inside its `SafeTxHash`
   computation) is a single incrementing counter per Safe. If two
   `PendingAction`s are proposed against the *same* Safe concurrently,
   both naively computed against "the Safe's current nonce," they
   collide the same way two Stellar transactions sharing a sequence
   number do - whichever executes second, after the first has already
   incremented the Safe's nonce, fails. This has no "pool" to reach for
   (there's only one Safe, one nonce sequence, at a time) - it needs
   **per-Safe serialization at proposal time**, not a shared resource.
2. **The relayer EOA(s) submitting `execTransaction` calls** (§13.7)
   have their own Ethereum account nonce. A single relayer processing
   many concurrent submissions across many different users' Safes hits
   *exactly* the problem channel accounts solve on Stellar - many
   pending on-chain submissions contending for one account's nonce
   sequence. This **does** need a pool, structured the same way the
   original's channel-account pool is.

**Design for risk 1 - per-Safe nonce reservation, held for the whole
pending window:**
- `PendingAction` gains a `SafeNonce` column, assigned atomically at
  proposal time: within one DB transaction, lock the `ClosedGroup` row
  (`SELECT ... FOR UPDATE` or GORM's equivalent), compute
  `nextNonce = safe.OnChainNonce + count(non-terminal PendingActions for
  this GroupID)`, insert the new `PendingAction` with that `SafeNonce`,
  commit. A concurrent proposal against the same Safe blocks on the row
  lock rather than racing - this is the correct version of what the
  original's `defer`-release pattern was supposed to provide and didn't.
- Released (in the sense of "no longer reserved") only when the
  `PendingAction` reaches a terminal state - `EXECUTED` or `REJECTED`.
  This closes another audited original gap: `RejectTransaction` never
  releases its channel account at all in the original, leaking it until
  the next restart's reconciliation; here, rejection is itself what
  frees the reservation, immediately, correctly.
- **New: a periodic expiry sweep for stale, never-resolved
  `PendingAction`s** - the original has this for fiat invoices
  (`ExpireStalePaymentInvoices`, a 30-minute ticker) but conspicuously
  *not* for `PendingAuth`/shared-access, a gap flagged in §13.1's audit
  and left unfixed there; this design closes it uniformly rather than
  leaving shared-access as the one flow with no timeout.

**Design for risk 2 - a relayer-EOA pool, directly mirroring the
original's channel-account pool:**

| Original (channel accounts) | Base equivalent (relayer pool) |
|---|---|
| `CHANNEL_ACCOUNTS` (seed CSV), `CHANNEL_ACCOUNT_MIN_COUNT`, `CHANNEL_ACCOUNT_FUNDER`, `CHANNEL_ACCOUNT_FUNDING_AMOUNT`/`MIN_BALANCE` | Equivalent env config for a pool of relayer EOAs - a funder key top-up, a minimum pool size, a minimum ETH balance per relayer |
| In-memory buffered channel + `InUseChannelAccounts` map + mutex | Same in-process shape is fine to start - a buffered channel of relayer keys plus an in-use set, guarded by a mutex |
| Claim = channel receive, release = channel send (correct in principle; the bug was *when* release was called, not the primitive itself) | Same primitive, held from claim until the submitted transaction is **confirmed on-chain** (or fails/times out) - not released the moment the call is merely broadcast, closing the analogous "released too early" risk before it can be introduced here |
| Startup goroutine scans `PendingAuth`/`FiatPaymentInvoice` rows with a pending status and re-marks their channel account in-use | Same pattern: scan for any transaction row still in a "submitted, not yet confirmed" state at boot and re-mark its relayer EOA in-use before accepting new work - the direct, explicitly-requested equivalent |
| Scoped narrowly to a few flows (shared access, regulated-asset minting, fiat purchase) - ordinary payments use the wallet's own account as source, sidestepping the relayer entirely | Scoped to **every** backend-submitted transaction, not narrowly - on Base, the relayer (not the wallet) always submits `execTransaction`, per §13.7's gas-sponsorship model, so the same nonce-contention risk exists uniformly, not just for multi-approval flows. Narrower scoping isn't available the way it is on Stellar. |

### 13.13 Open decisions for §13.12

- Pool sizing: static config (matching `CHANNEL_ACCOUNT_MIN_COUNT`) is
  the recommended starting point; auto-scaling the pool based on
  observed concurrent-submission volume is a reasonable later
  refinement, not needed for v1.
- Whether risk 2's pool should eventually move to a proper job-queue-
  backed relayer service (more robust under process restarts and
  horizontal scaling) rather than an in-process Go channel - the
  in-process version mirrors the original closely enough to start with
  and is simpler to reason about; flag if the operational scale from day
  one warrants the heavier design instead.
- Whether "confirmed on-chain" (risk 2's release condition) should wait
  for one confirmation or a small number, trading a slightly longer
  relayer-hold time for stronger protection against a reorg re-opening
  the same nonce.

Implementation does not begin until explicitly authorized.

## 14. Servicelinks: QR-driven login/2FA/event/payment-request parity

The original's servicelinks subsystem is **four** distinct client-facing
capabilities, not three - an earlier pass at this section only tracked
the `ApprovalKind` enum's three values (`LOGIN`/`AUTHORIZE`/`EVENT`) and
missed the fourth, differently-shaped one (payment-request links, §14.1a
below), since it isn't part of that enum at all. Status of each, audited
directly against the original's actual handlers:

| Capability | Original mechanism | Port status |
|---|---|---|
| **Login request → user login approval → verification** | `LOGIN`-kind approval; `VerifyApproval` calls the original's `LogUserIn`, issuing a real access/refresh token pair to the redeeming partner - see §14.1.1 for the full request→approve→verify lifecycle spelled out route-by-route | **Already correct** - confirmed in §12.5 above; the port's `VerifyApproval` does exactly this for `LOGIN` today. Only needs the `AudienceServiceLinkSession` rename (§12.5/§12.7 Phase 4), not a behavior change |
| **Authorize (payment authorization, 2FA, or any other partner-described consent)** | `AUTHORIZE`-kind approval; verify returns `{"message":"success"}`, no token - see §14.1.2 for the full request→approve→verify lifecycle, spelled out explicitly since "payment authorization" is a named use of this generic kind, not a separate mechanism | **Already correct** - confirmed in §12.5; no change needed |
| **Event** | `EVENT`-kind approval; backend supports it, but the original's own mobile app never wired a `'event'` case in its deep-link dispatcher | Backend: already correct. Mobile-side gap in the *original app itself* - `wallet-mobile` closes it (§14.2 item 5) |
| **Payment-request link** ("service link payment authorization") | **Not an approval at all** - see §14.1a. A stateless, Redis-cached QR/deep-link generator embedding a specific payment's destination/asset/amount/memo, requested by a partner on a named user's behalf. No `ServiceLinkApproval` row, no signature-based approve/verify step - the wallet owner reviews and signs the resulting payment themselves through the ordinary payment flow when they scan it | **Missing entirely from this port** - genuinely new work, §14.1a/§14.2 item 1 |

### 14.1 What's already correct (confirmed against the original's actual code)

The built `servicelinks` component already gets the important structural
decisions right for the three `ApprovalKind` capabilities, now confirmed
against the original rather than assumed: route separation
(`/v1/approvals/...` for the app side vs. API-key-authed `/v1/partner/...`
for the third party, never both on the same route - matching the
original's own split), the `ApprovalKind` discriminator itself
(`LOGIN`/`AUTHORIZE`/`EVENT`, matching the original's three kinds
exactly), and `VerifyApproval`'s behavior (see the table above). The
granular `CanLogin`/`CanRequestAuthorization`/`CanRegisterEvents`/...
capability flags on `ServiceLink` are, if anything, a cleaner design than
the original's own same-shaped-but-differently-named permission
booleans - no change needed there.

Spelled out step-by-step, since it's worth being unambiguous that both
of the following full lifecycles - not just the `VerifyApproval` end of
them - are already covered, route for route:

**14.1.1 Servicelinks login request → user login approval → partner
verify (the `LOGIN` lifecycle)**

| Step | Original route | Port's route (generic across all three kinds) |
|---|---|---|
| 1. Partner requests a login for a named user | `POST /v1/servicelinks/login/request/:targetUser` (`AuthenticationMiddlewareUsingAPIKey`) - creates a `ServiceLinkLoginSession` row | `POST /v1/partner/approvals` with `kind: "LOGIN"` (`APIKeyAuth`, gated on `CanLogin`) - creates a `ServiceLinkApproval{Kind: LOGIN}` row |
| 2. User approves in the app | `POST /v1/users/servicelinks/login/approval/:targetUser` (`AuthenticationMiddlewareUsingTimestamp` - `X-TW-SIGNER`/`X-TW-SIGNATURE`/`X-TW-TIMESTAMP`) - sets `Authorized = 1` | `POST /v1/approvals/:id/approve`, guarded by **§12's new per-request signature-verification middleware** (`X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp`, verified offline via `cryptoutil.VerifyPersonalSign` - §12.7 Phase 3 re-points this route here from `JWTAuth(..., AudienceWalletSession)`) - sets `Authorized = true` |
| 3. Partner redeems the approval | `GET /v1/servicelinks/login/verify/:ownerUsername/:targetUser/:loginID` (`APIKeyAuth`) - calls `LogUserIn`, returns an access/refresh token pair | `GET /v1/partner/approvals/:id/verify` (`APIKeyAuth`) - `VerifyApproval` mints the same kind of token pair for `LOGIN` (§12.5) |

This is the exact "users to authorize login requests... using our mobile
app to login third-party services" lifecycle from this redesign's
original brief - request, user approval, partner verification - and all
three steps already exist in the port today, just collapsed onto one
generic model/route set instead of the original's three near-identical
ones (a deliberate, already-made simplification, not a gap).

**14.1.2 Servicelinks payment/2FA authorization request → user approval
→ partner verify (the `AUTHORIZE` lifecycle - this *is* "servicelinks
user payment authorization")**

The original's `AUTHORIZE` kind is not 2FA-specific - it's a
general-purpose "ask the user, via the app, to approve this one
described thing" mechanism, and a partner requesting payment
authorization is exactly one of its uses (2FA and other consent prompts
being the others), not a separate feature:

| Step | Original route | Port's route |
|---|---|---|
| 1. Partner requests authorization (e.g. "approve this ₦5,000 payment to Merchant X", carried in `AuthDescription`) | `POST /v1/servicelinks/authorize/request/:targetUser` (`AuthenticationMiddlewareUsingAPIKey`) - creates a `ServiceLinkAuthorization` row | `POST /v1/partner/approvals` with `kind: "AUTHORIZE"` and the payment's description text in `Description` (`APIKeyAuth`, gated on `CanRequestAuthorization`) - creates a `ServiceLinkApproval{Kind: AUTHORIZE}` row |
| 2. User approves the payment/2FA/consent request in the app | `POST /v1/users/servicelinks/authorize/approval/:targetUser` (`AuthenticationMiddlewareUsingTimestamp` - `X-TW-SIGNER`/`X-TW-SIGNATURE`/`X-TW-TIMESTAMP`) - sets `Authorized = 1` | `POST /v1/approvals/:id/approve`, guarded by **§12's new per-request signature-verification middleware** (`X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp`, verified offline via `cryptoutil.VerifyPersonalSign`, no server-side session lookup) - sets `Authorized = true`. This is the specific step "user servicelink payment approval" refers to: the same route serves login approval, payment/2FA authorization approval, and event approval alike, all authenticated by this one middleware once §12.7 Phase 3 lands |
| 3. Partner (or the app itself, via the poll route) confirms | `GET /v1/servicelinks/authorize/verify/:ownerUsername/:targetUser/:authId` (partner, `APIKeyAuth`) / `GET /v1/servicelinks/app/authorize/verify/...` (app poll, signature-authed) - both return `{"message":"success"}`, no token | `GET /v1/partner/approvals/:id/verify` (`APIKeyAuth`) - returns the approval record, no token (§12.5) |

Nothing here moves or debits funds by itself - approving is consent, not
a transfer. If a partner's flow needs the user's wallet to actually move
funds after authorization (rather than just confirming intent to the
partner), that partner makes its own subsequent call against the ordinary
payment/shared-access endpoints using the now-confirmed consent, the same
way the original leaves it to the partner's own next call rather than
having `VerifyApproval` itself trigger a transfer. This `AUTHORIZE`
lifecycle is distinct from - and should not be confused with - §14.1a's
payment-*request* links, which involve no approval step at all.

### 14.1a Payment-request links (audited, not previously covered)

The original's `GET /v1/servicelinks/payment/request/:targetUser`
(app-signed) and `GET /v1/trovo-api/payment/request/:targetUser`
(API-key-only, for a partner calling server-to-server) are two entry
points to the *same* handler shape
(`dynamiclinks.GeneratePaymentData`): given `paymentDestination`,
`assetCode`, `assetIssuer`, `amount`, `memo` as query parameters (with
XLM-specific validation - asset code length, decimal truncation to 7
places, a 28-byte memo cap that will need Base-appropriate equivalents,
not identical numbers), it builds a dynamic link embedding
`action=payment` plus those same fields, renders it as a QR (cached in
Redis for 20 minutes so repeat requests for the same link are free), and
returns it - gated only by the requesting service link's
`PaymentPermission` flag. Two things worth being precise about, since
they're easy to get wrong porting this:
- **This is a "receive/request payment" QR, not an "authorize a payment"
  flow.** There is no server-side pending record and no separate verify/
  redeem step for a partner to poll - unlike `LOGIN`/`AUTHORIZE`/`EVENT`,
  nothing here waits for the user to approve anything on the backend.
  The mobile app's `processDeepLink` (`action == 'payment'`) simply
  pre-fills the ordinary send-payment screen from the decoded query
  parameters; the actual authorization is the user reviewing and signing
  that payment themselves through the normal payment endpoint, same as
  if they'd typed the destination in by hand. Do not build a
  `ServiceLinkApproval`-style record for this - it would be inventing
  state the original never has.
  - **A dead/commented-out check worth not reviving**: the original's
    handler for the app-signed variant has a commented-out
    `mInfo.PublicKey != middleware.ExtractPublicKey(c)` identity check -
    i.e. even the "signed" route never actually verifies the caller's
    identity against anything beyond the service link's own API key.
    The Base port's version doesn't need to reproduce this dead code
    either way, but should decide deliberately whether the signed
    variant should check the caller matches `targetUser` (tighter than
    the original) rather than silently inheriting the original's
    no-op check.

**Header/verification note, stated explicitly rather than left
implicit**: `/v1/approvals/...` is one of the routes §12.7 Phase 3
re-points from `JWTAuth(..., AudienceWalletSession)` onto the new
middleware - once that phase lands, the app side of servicelinks
authenticates the exact same stateless way as every other user route:
`X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp` (§12.2),
verified offline against `cryptoutil.VerifyPersonalSign` with no
server-side session lookup (§12.3) - there is no separate carve-out or
leftover JWT path for servicelinks specifically. `/v1/partner/...` keeps
`APIKeyAuth` unchanged (§12.5) - that side was never part of the
JWT-session scheme to begin with, so nothing there moves.

### 14.2 Gaps to close

1. **Payment-request links don't exist in this port at all** (§14.1a) -
   add a route pair mirroring the original's two entry points (app-signed
   via §12's new middleware; partner-side via the existing `APIKeyAuth`),
   gated on a `CanSendPayments`-style capability flag (the port's
   `ServiceLink` model already has `CanSendPayments`/`CanReadBalances` -
   reuse rather than add a redundant flag), reusing the same
   shortlink/QR mechanism from item 2 below and encoding
   `to`/`tokenAddress`/`amount`/`memo` in EVM terms instead of
   `paymentDestination`/`assetCode`/`assetIssuer`/`amount`/`memo` in
   Stellar terms. No `ServiceLinkApproval` row - this stays a stateless,
   cached link generator per §14.1a.
2. **QR code generation is entirely missing from this component.** The
   original generates a dynamic link embedding an `action` query
   parameter (`login`/`authorize`/`event`/`payment`) plus the relevant
   ID(s), then renders that link as a QR PNG (`internal/dynamiclinks`) -
   this `action` parameter is the *only* thing the original mobile app
   actually reads to decide which approval screen to show (confirmed -
   there is no separate "get approval details" endpoint that returns a
   kind field). This port already has an equivalent QR/short-link
   component built for an unrelated purpose
   (`internal/components/shortlink`, Phase 13) - the fix is to reuse it,
   not build QR support twice: on `RequestApproval` (and on item 1's new
   payment-request handler), mint a shortlink whose target encodes
   `action=login|authorize|event|payment` plus the relevant ID(s), and
   return that shortlink's QR image URL alongside the response.
3. **No callback dispatch.** `ServiceLinkApproval.CallbackURL` is stored
   but never read back anywhere in the reviewed code - the original
   fires an async, retried webhook POST to the partner's callback URL
   the moment a user approves (`callBackRetryChan`). Add the same:
   on `Approve`, dispatch the callback asynchronously with retry: this
   is the piece that lets a partner learn "approved" without polling
   `verify` in a loop.
4. **`AudienceServiceLinkSession` rename** - tracked already in §12.5/
   §12.7 Phase 4, not a new item, just cross-referenced here since it's
   this component's file that changes.
5. **`EVENT` action on the mobile side is untested territory even in
   the original** - the original mobile app's own deep-link dispatcher
   (`processDeepLink` in `storage/state.dart`) has no `'event'` case at
   all; only `login`/`payment`/`authorize`/`register`/`tokenizedAsset`
   are wired, despite the backend having a real `EVENT` kind. This is a
   gap in the *original's own mobile app*, not something to reproduce -
   `wallet-mobile` (see its own `PLAN.md`) will add the missing case
   properly rather than carry the original's oversight forward.

### 14.3 Phased roadmap

| Phase | Scope | Depends on |
|---|---|---|
| 1 | New payment-request-link route pair (§14.2 item 1) - EVM-equivalent query params, `CanSendPayments` gate, no approval record | — |
| 2 | Wire `RequestApproval` (and Phase 1's handler) to mint a shortlink + QR via the existing `shortlink` component, embedding `action`+IDs the same way the original's dynamic links do | §12.7 Phase 3 not required, but naturally lands alongside it |
| 3 | Async, retried callback dispatch on `Approve` | Phase 2 |
| 4 | `AudienceServiceLinkSession` rename (shared with §12.7 Phase 4) | §12.7 Phase 3 |
| 5 | Test, document, push | 1-4 |

#### Phases 1-4 implementation notes (done)

All four phases were implemented together, since the payment-request-link
handlers and `RequestApproval`'s QR minting share the same
`mintDeepLink` helper and naturally landed as one change:

- **`internal/components/servicelinks/services/deeplinks.go`** (new) -
  `mintDeepLink(action, params, metadata)` builds a target URL of the
  form `{ShortlinkBaseURL}/deeplink?action=<action>&<params>`, mints it
  via `s.Shortlink.CreateLink`, and returns the resulting short URL plus
  its QR image URL (`shortURL + "/qr"`, the existing route shape from
  `shortlink/controllers.go`). `action` is embedded as a query parameter
  exactly as the original's dynamic links did, since that is the only
  field the original mobile app's deep-link dispatcher reads. If
  `Service.Shortlink` is nil (not wired up), it is a no-op returning a
  zero-value result rather than an error - this keeps every existing
  test that doesn't care about QR minting unaffected, and keeps
  `RequestApproval`'s behavior optional-but-additive rather than a hard
  new dependency.
- **`Service.Shortlink *shortlinkServices.Service`** (new field,
  `services.go`) - assigned post-construction in `main.go`
  (`servicelinksSvc.Shortlink = shortlinkSvc`, after
  `shortlinkControllers.Init` runs), the same cross-component wiring
  pattern as `paymentsSvc.Alerts`/`usersSvc.GeoIP`/
  `sharedaccessSvc.Assets`. This was chosen over adding a fifth
  constructor argument to `New` (and reordering `main.go` so `shortlink`
  initializes first) to avoid touching every existing test's `New(...)`
  call site for an optional, late-bound dependency - consistent with how
  `usersSvc.GeoIP` (also optional) is wired, not with how the four
  *required* peer services are wired as constructor args.
- **`RequestApproval`** (`services/approvals.go`) now calls
  `mintDeepLink` after creating the `ServiceLinkApproval` row, using
  `action = strings.ToLower(string(input.Kind))` (`login`/`authorize`/
  `event`) and `id = approval.ID`. The result populates two new
  **transient** fields on `models.ServiceLinkApproval`: `ShortURL` and
  `QRURL`, both tagged `gorm:"-"` (never persisted - they're minted
  fresh every call and would go stale as soon as read back from a stored
  row). The controller's existing `c.JSON(http.StatusCreated, approval)`
  picks these up automatically since they're just additional JSON fields
  on the same struct - no controller/response-shape change was needed.
- **Payment-request links** (`services/payment_requests.go`, new) - a
  deliberately stateless `PaymentRequestLink{To, TokenAddress, Amount,
  Memo, ShortURL, QRURL}` with no `ServiceLinkApproval`-style DB row, per
  §14.1a. Two entry points, mirroring the original's app-signed/partner-
  API-key pair:
  - `RequestPaymentLink(to, tokenAddress, amount, memo)` - the app-signed
    half. **Deliberate design decision, stated explicitly since PLAN.md
    §14.1a flagged it as worth deciding rather than silently inheriting**:
    this has no `targetUser`/`:id` parameter and no `CanSendPayments`
    gate at all. The original's app-signed route took a `:targetUser`
    path parameter but its only identity check was commented-out dead
    code (`mInfo.PublicKey != middleware.ExtractPublicKey(c)`), meaning
    it never actually verified the caller matched the named target. Per
    §14.1a's own framing, reviving that no-op check would add nothing;
    instead this route only ever mints a link for the caller's own use
    (there is no separate target to name), which is strictly tighter
    than the original's unenforced version. It has no `CanSendPayments`
    gate because that capability lives on a `ServiceLink`, and this
    app-signed path carries no service-link/partner context to check it
    against - it's an ordinary self-service wallet feature ("generate my
    own receive-payment QR"), not a partner acting on someone's behalf.
  - `RequestPaymentLinkForOwnedUser(serviceLinkID, userID, to,
    tokenAddress, amount, memo)` - the partner-side half, gated on
    `CanSendPayments` and scoped through the existing `requireOwnedUser`
    exactly like every other route in `controllers/payments.go`. This is
    a deliberate *tightening* versus the original, whose equivalent route
    had no ownership check on the named target user at all - consistent
    with this port's established "deny by default, scope to owned users"
    posture for every other partner route (PLAN.md §4.11 findings 5/6),
    even though PLAN.md §14.1a's audit of the original didn't specifically
    flag this route's ownership gap (the original's real bug there was
    the dead identity check, not this).
  - In both cases `to`/`tokenAddress`/`amount`/`memo` are supplied
    explicitly by the caller (mirroring the original's own
    `paymentDestination`/`assetCode`/`assetIssuer`/`amount`/`memo` query
    parameters) rather than derived from the named user's stored address
    - the destination and "who this request is for" are independent, the
    same as the original (e.g. a merchant requesting payment to its own
    treasury address on a customer's behalf).
  - Routes: `GET /v1/payment-requests` (new top-level group, deliberately
    *not* nested under `/v1/approvals` since PLAN.md §14.1a is explicit
    this "is not an approval at all", `SignatureAuth`-protected) and
    `GET /v1/partner/users/:userId/payment-request` (added to
    `registerPaymentRoutes` in `controllers/payments.go`, `APIKeyAuth`-
    protected). Both take `to`/`tokenAddress`/`amount`/`memo` as query
    parameters (a GET, not a POST-with-body, to mirror the original's own
    query-param shape and because minting a link is naturally idempotent
    on its inputs).
  - **Scope cut, stated explicitly rather than silently dropped**: the
    original cached the generated QR in Redis for 20 minutes so repeat
    requests for the same link were free. This port has no Redis
    dependency anywhere else (a deliberate choice made much earlier in
    this port - see `internal/components/shortlink`'s own package doc
    comment on "free/local default" integrations), and `DynamicLink` rows
    are cheap, permanent, and already deduplicated by nothing needing to
    match - re-minting a fresh short code on every call is simpler than
    introducing a cache layer for one route, at the cost of one extra
    small DB row per request. If this route sees enough traffic for that
    to matter in practice, a content-addressed cache key (hash of
    to/tokenAddress/amount/memo) could dedupe short codes later without
    changing the public response shape.
- **Async retried callback dispatch** (`services/callbacks.go`, new) -
  `dispatchApprovalCallback(approval)` POSTs a small JSON payload
  (`approvalId`/`kind`/`targetUserId`/`authorized`) to
  `approval.CallbackURL`, retrying with a fixed backoff schedule
  (`1s, 5s, 15s, 30s` between the 5 total attempts) on any non-2xx
  response or transport error, and only logging (never returning an
  error to the caller) once every attempt is exhausted. Called as
  `go dispatchApprovalCallback(*approval)` from `Approve`, immediately
  after the approval row is saved, so a slow or unreachable partner
  endpoint can never block or fail the user's own approve request.
  **Scope note, stated explicitly**: the original used a channel-based
  `callBackRetryChan` worker; this codebase has no existing generic
  retried-webhook dispatcher anywhere to mirror that structure against
  (confirmed by research across `internal/notify` and
  `internal/alerting`, both synchronous/best-effort/no-retry), so this
  is a small fixed-attempt/fixed-backoff loop rather than a literal port
  of the channel design - functionally equivalent (bounded retries with
  backoff, fire-and-forget from the caller's perspective), differently
  shaped.
- **Tests** (`services/payment_requests_test.go`,
  `services/callbacks_test.go`, plus two new cases folded into
  `deeplinks`' consumer `RequestApproval`): QR/short-URL minting on
  `RequestApproval` (and its no-op behavior when `Shortlink` is nil);
  `RequestPaymentLink`'s required-fields validation and successful
  minting; `RequestPaymentLinkForOwnedUser`'s ownership scoping (rejects
  an organic user, accepts an owned one); `dispatchApprovalCallback`'s
  successful-delivery, retry-then-succeed, and exhausted-retries paths
  using `httptest.Server` (with `approvalCallbackBackoff` temporarily
  shrunk to millisecond durations so the retry tests run fast); and an
  end-to-end `Approve` test asserting the callback is actually received
  by a real local HTTP server. `newTestService` (`services_test.go`) now
  also constructs a real `shortlinkServices.Service` against the same
  in-memory test DB and wires it as `svc.Shortlink`, so every existing
  test that calls `RequestApproval` now exercises the real QR-minting
  path rather than a stub.
- Full module `go build ./...`, `go vet ./...`, and `go test ./...`
  (including `-race` on the servicelinks package specifically) all pass.
  `go mod tidy` made no changes - no new dependencies were needed.
- **§14 Phase 4 (`AudienceServiceLinkSession` rename) was already done**
  in an earlier session (task #133 / §12.7 Phase 4) - confirmed still
  live in `internal/middleware/jwt_auth.go` and unaffected by this work.

Implementation does not begin until explicitly authorized.

## 15. Wallet recovery: two coexisting branches, not a replacement

**Explicit decision, superseding an earlier draft of this section**: the
already-built `recovery.go` (DB-only - re-points a username's address to
a caller-supplied new one, no chain interaction, no sub-wallet/shared-
access continuity) is **kept exactly as it is**, not superseded. A
second, new mechanism is added alongside it - true wallet-signer
recovery, preserving the same address via a Safe owner-swap. Both
branches share the same identity-proof front door (security questions +
email OTP + a `personal_sign` from the new key) and diverge only in what
happens once that proof succeeds:

| | Branch A - DB-only address swap | Branch B - true wallet recovery |
|---|---|---|
| What it does | `UPDATE users SET address = newAddress` - a fresh identity, nothing preserved | `swapOwner` on the primary wallet's Safe - same address, same funds, same sub-wallets and shared-access memberships |
| Already built? | **Yes** - `recovery.go`, unchanged by this section | No - new work, §15.5 below |
| Cost | Free (matches what's already built - no fee logic exists in `recovery.go` today) | The original's one-off enrollment fee (§15.7) |
| Requires primary wallet already deployed? | No | Yes (§13.11) |
| Enrollment flag | `User.AccountRecoveryEnabled` (already built, already wired) | New, separate flag - §15.2a |

This mirrors the original's own two-function split
(`DoInactiveAccountRecover` vs. `DoAccountRecovery`, §15.1) more
faithfully than a single mechanism could - the original also offers a
cheap, identity-only path and a stronger, wallet-preserving path side by
side, not one replacing the other.

### 15.1 What the original actually does (audited directly)

`internal/components/users/services/account_recovery.go` (~1000 lines:
`EnableAccountRecovery`, `DisableAccountRecovery`, `DoAccountRecovery`,
`DoInactiveAccountRecover`):

- **Enrollment** (`EnableAccountRecovery`): a deterministic, per-user
  recovery keypair - derived from one global secret
  (`MNEMONIC_ACCOUNT_RECOVERY` plus salts, never stored per-user, see
  `blockchainalgofuncs/algofuncs.go:13-35`) - is added as a weight-1
  `SetOptions` signer to the primary wallet and every sub-wallet that
  either has no shared access, or has shared access enabled with
  `NumberOfApprovalsNeeded == 0` (i.e. no real independent approver
  exists yet - confirming the user's own framing precisely).
  Market-making/bulk-payment wallets (already-custodial, higher-threshold
  wallet types) get the recovery signer at weight 3 instead of 1, to
  match their own higher operating threshold rather than being a fixed
  constant. Wallets under genuine N-of-M shared access are explicitly
  skipped - recovery never unilaterally inserts itself into a wallet
  other real parties jointly control.
- **Execution** (`DoAccountRecovery`): identity is proven **not** by
  anything from the lost key - by three factors instead: every
  configured security answer, a valid emailed OTP, and (implicitly,
  since the caller must supply it) a new signer address. The recovery
  keypair itself then submits a swap (add the new signer at weight 1,
  then remove the old one) on the primary wallet and every eligible
  wallet, deletes the recovering user's `APPROVER` grants on *other*
  people's wallets (their old identity can no longer honor them, and the
  original does not attempt to transfer them), and requires an explicit
  flag to actually remove the old signer from the *primary* wallet - by
  default the lost key stays valid there even after "recovery."
- **Real bugs in the original, flagged rather than reproduced**:
  the eligibility/removal boundary is checked inconsistently across
  three different call sites - `EnableAccountRecovery` and
  `DoAccountRecovery` use `NumberOfApprovalsNeeded == 0`,
  `generateCreateSharedAccessXdr` (in `shared_access.go`) uses `> 1`
  approvers, `generateModifySharedAccessXdr` uses `> 0` - meaning a
  wallet can end up with `NumberOfApprovalsNeeded == 1` while the
  recovery signer is still live on it, a real drift between the DB's
  intent and the chain's actual signer set. Separately, the per-request
  signature middleware on `DoAccountRecovery`/`DoInactiveAccountRecover`
  is "decorative" - it verifies *a* valid signature exists, but never
  binds the signer to `payload.Username`, since by construction the
  caller may not control any registered signer at all. The actual gate
  on those two routes is the security-answer/OTP check alone.

### 15.2 Why Branch B is new work, not an extension of `recovery.go`

`recovery.go`'s own doc comment states its design assumption directly:
*"unlike a Stellar account, an EVM address is its key, so there is no
're-key the same address' operation to perform"* - correct for a bare
EOA, and exactly what §13's Safe redesign changes: once the primary
wallet is a Safe (§13.3), re-keying the same address becomes possible on
Base too, via `swapOwner`. That's a different code path from `recovery.go`'s
`UPDATE users SET address = newAddress`, not a variant of it - hence a
new branch rather than a modification. `recovery.go`'s security-question/
OTP/new-address-`personal_sign` machinery is directly reusable as
Branch B's *identity-proof front door* too (§15.5 step 1) - that part is
shared code, not duplicated.

**§13.3's nested-EIP-1271-ownership design also collapses the original's
whole "which wallets are eligible" question, for Branch B specifically.**
Because every sub-wallet and every shared-access `GroupMember` entry
names a participant's *primary wallet address*, never their signer key
directly (§13.4), swapping the signer on **just the primary wallet's
Safe** is instantly, transparently reflected everywhere that user is
referenced - every sub-wallet they own, and every shared-access wallet
they participate in as owner or approver - with **zero** additional
on-chain transactions and **no per-wallet eligibility logic at all**.
This is not an approximation of the original's intent, it's a strictly
cleaner realization of it: the original has to loop over every wallet
and individually decide whether to touch it (and, per §15.1, sometimes
gets that boundary wrong); Branch B has nothing to loop over, because
there's only ever one place a signer swap needs to happen.

### 15.2a Two independent enrollment flags, not one gating the other

**Recommendation**: `User.AccountRecoveryEnabled` (already built, gates
Branch A exactly as today) and a new, separate `User.WalletRecoveryEnabled`
(gates Branch B) are independent - a user may enable neither, either, or
both, and enabling one never requires or implies the other. This is the
simplest option and avoids inventing a dependency the original doesn't
have either (its `EnableAccountRecovery` is a single all-or-nothing
switch for its one mechanism). The alternative - requiring Branch A's
flag before Branch B's can be set, on the theory that Branch B is a
"stronger upgrade" - is a documented option, not the recommendation;
flag if that dependency is wanted instead before implementation starts.

### 15.3 Branch B's recovery service: what it can do, stated plainly

Enabling Branch B means adding a second owner to the primary wallet's
Safe: `owners: [signerEOA, recoveryServiceAddress], threshold: 1`. Since
Safe has no per-owner "this key may only do X" restriction natively,
this owner - like the original's weight-1 Stellar signer on a
threshold-1 account - can authorize *anything* on the primary wallet,
not just a signer swap, for as long as it remains an owner. This is the
same centralization tradeoff the original has (a paid recovery service
is, necessarily, a party capable of real signing power), not a new one
introduced by porting it - but Base's programmability offers a genuine
improvement the original's ledger cannot:

- **Recommendation: put a Safe [Guard](https://docs.safe.global/advanced/smart-account-guards)
  on the primary wallet that restricts what a transaction co-signed by
  the recovery service's owner slot may call** - permitting only
  owner-management functions (`swapOwner`, and nothing that transfers
  value or calls arbitrary contracts) whenever the recovery service is
  one of the signers on that transaction. This narrows the recovery
  service's real-world blast radius from "can do anything the user could
  do" down to "can only ever rotate the signer," a strictly stronger
  guarantee than the original provides, made possible by Base's
  programmability rather than something Stellar's native multisig could
  express. Worth building even though it's not a parity requirement.
- **Recommendation: the recovery service's own address should itself be
  a Safe with a robust internal N-of-M among the platform's own trusted
  operators** (or HSM-backed signers), not a single hot key - referenced
  on every enrolled primary wallet via the same nested EIP-1271
  mechanic used everywhere else in this design (§13.3), rather than the
  original's single global-secret-derived keypair (`MNEMONIC_ACCOUNT_
  RECOVERY` compromise today would compromise every enrolled user
  simultaneously - the same single-point-of-failure shape already
  flagged for the pre-redesign `sharedaccess` component in §13.5).

### 15.4 Branch B eligibility, simplified

Given §15.2's nested-ownership point, eligibility stops being "which
wallets does recovery need to touch" (the original's error-prone
question) and becomes "does the *primary* wallet's own Safe already have
a threshold the primary's single owner can satisfy alone" - which is
always true for this design (`threshold: 1` from creation, §13.3) unless
the primary wallet itself has been given additional independent owners
some other way (not part of this plan's current scope). **Branch B
enrollment is therefore a single operation on a single Safe, full
stop** - no per-sub-wallet, per-shared-wallet loop, no
`NumberOfApprovalsNeeded` boundary check, and consequently none of
§15.1's boundary-inconsistency bug surface exists to reproduce.
Market-making/bulk-payment-style higher-threshold custodial wallets
(§13.1's `WalletType 2/3`) are simply out of scope for Branch B under
this design, by construction - not because of a special-cased exclusion
rule, but because they were never reached through the primary wallet's
owner chain to begin with. Flagging this as a deliberate simplification
worth confirming rather than a gap. (Branch A has no such restriction -
it never touches any wallet but the DB row, so wallet type is
irrelevant to it.)

### 15.5 Branch B: true wallet recovery execution flow

1. Locked-out user proves identity via the **same** functions
   `recovery.go` already exports for Branch A - every configured
   security answer, a valid emailed OTP, and a `personal_sign` proof
   from the **new** signer key over a recovery message (never from the
   old one - it's lost, by definition). This is shared, called code, not
   a second copy of the logic - see §15.8.
2. Where Branch A's `Recover` does `UPDATE users SET address =
   newAddress` (which changes the wallet's permanent identity), Branch B
   instead builds and submits a Safe `swapOwner(prevOwner, oldSigner,
   newSigner)` call against the **primary wallet's Safe** - `User.Address`
   never changes; only `User.SignerAddress` does, in step with the
   Safe's actual on-chain owner. This is a distinct new function,
   `RecoverWallet` or similar, called from a distinct new route -
   `recovery.go`'s existing `Recover` is untouched.
3. Per §15.4, no other wallet needs any on-chain transaction. The
   recovering user's `GroupMember` rows on *other* people's shared-access
   wallets (where they hold `APPROVER`) don't need deleting either -
   unlike the original, those entries reference the recovering user's
   *primary wallet address*, which hasn't changed, so they keep working
   automatically the next time that wallet's owner resolves through
   EIP-1271. This is a genuine behavioral improvement worth calling out:
   the original manually revokes those grants (`account_recovery.go`
   lines 825-846) specifically *because* it re-points the whole identity
   to a new address; this design doesn't create that problem in the
   first place.
4. Whether to force-remove the old signer from the primary wallet, or
   leave it valid alongside the new one (the original's actual default
   behavior, gated behind a flag it defaults to skipping): **recommend
   always removing the old signer as part of the swap** rather than
   making it optional - a "recovery" that can leave a known-lost key
   still authorized is a materially weaker guarantee, and `swapOwner`
   makes atomic remove-and-add a single call rather than the original's
   two separate ops, so there's no operational reason left to keep it
   optional.

### 15.6 Interaction with §12's authorization model

Both branches' execute routes (Branch A's already-built
`POST /v1/account-recovery/:username/recover`, and Branch B's new
equivalent - §15.9 names it `.../recover-wallet`) are a deliberate
exception to §12's general rule (self-service, or delegated via a
`GroupMember` role): the whole premise is that the caller does **not**
control any address currently authorized on the target account. Each
route still parses a well-formed §12-style signature (so the HTTP layer
is uniform), but that signature only has to be internally valid, not
tied to the target account - authorization for *these* routes comes
entirely from the shared identity factors (§15.5 step 1), exactly
matching the original's actual (if under-documented) behavior audited in
§15.1 and already how the built Branch A route behaves today. Enable/
Disable, for both branches, **do** fit §12's normal model cleanly (the
caller must already be the account's current signer) and need no
exception.

### 15.7 Fee model

**Branch A stays free**, matching what's already built - no fee logic
exists in `recovery.go` today, and this section doesn't propose adding
any. **Branch B carries the original's one-off enrollment fee**
(`ServiceFee`-style, matching `ACCOUNT_RECOVERY_FEE`) - not an enforced
subscription; `AccountRecoveryExpiresOn` is set in the original but never
read back anywhere in its own codebase (a half-built, never-finished
renewal feature). Recommendation: port only what's actually enforced for
Branch B - a one-off enrollment fee - rather than building unused
expiry/renewal machinery to match a field the original itself never
wired up.

### 15.8 What happens to the existing `recovery.go`

**Nothing - it is Branch A, unchanged, in full.** Its OTP model
(`AccountRecoveryEmailVerification`), security-question verification
(`verifyAllSecurityAnswers`), `personal_sign`-proof-of-the-new-address
pattern (`verifyNewAddressOwnership`/`RecoveryMessage`), and its
`Recover` function's actual DB-swap behavior all stay exactly as built -
this is Branch A's complete implementation, not a partial one awaiting
replacement. Branch B is purely additive: new fields
(`User.WalletRecoveryEnabled`, §15.2a), a new service function
alongside `Recover` (not instead of it) that reuses
`verifyAllSecurityAnswers`/`consumeValidOTP`/`verifyNewAddressOwnership`
as shared, called code and then performs the `swapOwner` call from
§15.5 step 2 instead of `Recover`'s DB update, and new routes (§15.9).
`buildRecoveryLog`'s tamper-evident attestation pattern (signed by a
`cryptoutil.DeriveKey`-derived authority key) is worth reusing for
Branch B's own audit trail too, logging a different fact (a signer swap
rather than an address change).

### 15.9 Phased roadmap (not started - design only)

**Branch A needs no build phase - it's already shipped.** The phases
below are Branch B only, purely additive to the existing `recovery.go`:

| Phase | Scope | Depends on |
|---|---|---|
| 1 | Recovery-service Safe: its own internal N-of-M ownership, deployed once as platform infrastructure | §13's Safe wiring |
| 2 | Safe Guard contract restricting the recovery-service owner to owner-management calls only (§15.3) | Phase 1 |
| 3 | `User.WalletRecoveryEnabled` field; enable/disable service functions and routes: add/remove the recovery-service Safe as a second owner of the caller's primary wallet, gated by the one-off fee (§15.7) - independent of `User.AccountRecoveryEnabled` per §15.2a | §13.11 (primary wallet must already be deployed) |
| 4 | New `RecoverWallet`-equivalent service function alongside (not replacing) `Recover`: reuses `verifyAllSecurityAnswers`/`consumeValidOTP`/`verifyNewAddressOwnership` as shared code, then performs `swapOwner` per §15.5 step 2; new route (e.g. `POST /v1/account-recovery/:username/recover-wallet`), `recovery.go`'s existing route and `Recover` untouched | Phase 3 |
| 5 | Optional: a `DoInactiveAccountRecover`-equivalent for a never-yet-activated primary wallet, if wanted as a *third* path distinct from Branch A/B - worth confirming it isn't already redundant with Branch A (which already handles "no chain cost" identity recovery unconditionally, without the original's extra never-activated/fresh-account restriction) before building a third mechanism | — |
| 6 | Test, document, push | 1-4 (5 if pursued) |

#### Phases 1-4 implementation notes (done)

All four phases were implemented and pushed together as one coherent
change (Phase 5 was deliberately not pursued - see its own note below).

- **Guard contract** (`internal/contracts/solidity/RecoveryGuard.sol`,
  new) - a from-scratch Solidity contract implementing Safe's real
  `Guard` interface (v1.4.1): `checkTransaction` reverts whenever a
  transaction was co-signed by the recovery-service owner slot *and*
  either targets something other than the Safe's own address, carries a
  non-zero value, or calls a selector outside
  `swapOwner`/`addOwnerWithThreshold`/`removeOwner`/`changeThreshold`;
  transactions the recovery service did not co-sign are never touched.
  "Co-signed by the recovery service" is detected by scanning the packed
  signatures blob for a contract-signature slot (`v == 0`) whose `r`
  equals the recovery-service address - correct without any `ecrecover`
  because the recovery service is by design always a Safe (a contract),
  never a raw EOA, so its contribution to another Safe's signature blob
  is always the EIP-1271 contract-signature form (see
  `internal/safe/signatures.go`'s `PackSignatures`).
  - **Verified two ways, given the risk of shipping a subtly-wrong
    security-critical contract**: (1) compiled successfully with solc
    `0.8.24` (installed fresh in this sandbox via `npm install
    solc@0.8.24`, confirmed network-reachable) against the exact
    interface declared in the `.sol` file; (2) `RecoveryGuard`'s declared
    `Guard` interface's ERC-165 `interfaceId` was independently
    recomputed from scratch in Go (`crypto.Keccak256` over each
    function's canonical signature string, XORed) and confirmed to match
    Safe's real, well-known Guard interfaceId (`0xe6d7a83a`) exactly -
    see `solidity/README.md`'s new note. This is strong evidence the
    interface is selector-for-selector identical to Safe's real one
    (`operation` declared as `uint8` rather than importing Safe's own
    `Enum.Operation` type - ABI-equivalent, since Solidity canonicalizes
    an enum parameter to `uint8` in a signature either way), which is
    exactly what `Safe.setGuard` checks before installing a guard.
  - **Scope note, stated explicitly**: a full simulated-EVM functional
    test (deploying the compiled bytecode to an in-process chain and
    calling `checkTransaction` against various inputs) was attempted
    first, using go-ethereum's `ethclient/simulated` package - it works,
    but pulls in a large, otherwise-unneeded transitive dependency tree
    (go-ethereum's own beacon/catalyst simulation stack, ~180 lines of
    new `go.mod`/`go.sum` entries) disproportionate to testing one guard
    contract, so it was reverted in favor of the encode/decode-level
    verification `internal/contracts/recovery_guard_test.go` actually
    ships with - the same posture `contracts_test.go` already documents
    for `TokenizedAsset`/`Sale` ("deploying and calling these contracts
    end-to-end needs a real or simulated EVM, which is outside this
    package's test scope"). Compiling with real solc plus the
    independent interfaceId cross-check are both *stronger* verification
    than that existing precedent had, just stopping short of a live
    execution test.
  - `internal/safe/safe.go` gained `EncodeSwapOwnerCalldata`/
    `EncodeSetGuardCalldata` (Safe's own `swapOwner`/`setGuard`
    self-calls), tested the same way as every other `Encode*Calldata`
    helper in that file (pack, then unpack against the embedded ABI).
- **Recovery-service Safe + platform deployment**
  (`internal/components/users/services/recovery_platform.go`, new):
  `RecoveryOperatorKeySalts []string`/`RecoveryServiceThreshold int` are
  new `Service` fields (assigned post-construction in
  `controllers.Init`, matching `usersSvc.GeoIP`'s pattern) - each salt
  derives one platform trusted operator's key via
  `cryptoutil.DeriveKey(salt + "|recovery-operator")`, **genuinely
  distinct secrets**, not the same salt reused with role suffixes like
  every other derived key in this codebase, since an N-of-M scheme
  tracing back to one underlying secret defeats its own purpose.
  `ComputeRecoveryServiceSafeAddress` mirrors `computePrimaryWalletAddress`'s
  own CREATE2-address-before-deployment pattern (owners = every operator,
  threshold = configured value or a majority default).
  `EnsureRecoveryPlatformDeployed(ctx)` idempotently deploys the
  recovery-service Safe (via the existing `SafeProxyFactory` path) and
  then the `RecoveryGuard` contract (via a plain
  `network.Client.DeployContract` CREATE, since it isn't a Safe), saving
  progress after each step so a Guard-deployment failure never causes a
  retry to resubmit an already-succeeded Safe deployment. State lives in
  a new singleton row, `models.RecoveryPlatformInfrastructure` (`ID` = 1)
  - the Guard's address genuinely cannot be recomputed the way the Safe's
    can (plain `CREATE` depends on deployer-nonce history), so it must be
    persisted to be found again.
  - **Deliberately opt-in**: `RECOVERY_OPERATOR_KEY_SALTS` defaults to
    empty (unlike every other `*_KEY_SALT` in this codebase, which
    default to a `dev-only-change-me` placeholder) - a non-empty default
    would silently attempt to deploy Branch B's platform infrastructure
    for every deployment of this codebase, including ones that never
    intend to offer wallet-signer recovery at all.
    `EnsureRecoveryPlatformDeployed` is called at boot
    (`main.go`, before the router serves traffic, same placement as
    `sharedaccessSvc.ReconcileRelayers`) and is a logged no-op when
    unconfigured. A deployment failure is logged, not fatal - the same
    non-blocking posture `ReconcileRelayers` itself takes, since this is
    one optional feature among many components this server hosts.
- **Enrollment** (`internal/components/users/services/wallet_recovery.go`,
  new): `BuildEnableWalletRecovery`/`ConfirmEnableWalletRecovery` and
  their `Disable` mirrors. Enabling performs two sequential
  self-management Safe transactions on the caller's own primary wallet -
  `addOwnerWithThreshold(recoveryServiceAddress, 1)` then
  `setGuard(guardAddress)` - each requiring the caller's own personal_sign
  signature over its `SafeTxHash` (verified via the same
  `cryptoutil.VerifyPersonalSignBytes` every other Safe-transaction
  approval in this codebase uses), then submitted by a dedicated
  server-controlled key (`deriveRecoveryPlatformDeployerKey`, a new role
  suffix on the existing `SafeDeployerKeySalt`) - submitting a call with
  valid signatures already attached grants the submitter no authority
  over the Safe, the same permissionless-relay reasoning
  `DeployPrimaryWallet` already relies on for factory deployments.
  - **Scope decision, stated explicitly**: this does *not* reuse
    `sharedaccess`'s relayer pool or its row-locked nonce-reservation
    invariant (PLAN.md §13.12 Phase 6) - both exist to solve concurrent
    multi-approver group actions, which don't apply here (a solo user
    enrolling their own wallet, one enrollment at a time). Instead
    `enableWalletRecoveryTxs`/`disableWalletRecoveryTxs` read the Safe's
    current nonce fresh on every call; if it drifts between Build and
    Confirm, the mismatch surfaces as an ordinary signature-verification
    failure (the client just re-Builds), a smaller, accepted race
    appropriate to this flow's much lower concurrency, not a silent gap.
    Also: importing `sharedaccess` from `users` would create an import
    cycle (`sharedaccess` already imports `users` to resolve members'
    primary wallet addresses), so this had to be self-contained in
    `users/services` regardless, built directly on the same `internal/safe`
    primitives sharedaccess itself uses.
  - The one-off enrollment fee (§15.7, `WalletRecoveryFeeWei`, zero by
    default) is a plain native-ETH transfer built via
    `BlockchainClient.BuildNativeTransferTx` to a derived fee wallet
    (`RecoveryAuthoritySalt + "|wallet-recovery-fee-wallet"`) - the
    caller signs and submits it themselves, and `feeTxHash` is trusted
    once supplied, the same posture this codebase already takes for
    every other self-submitted on-chain payment (see patron's
    `ConfirmSubscription`).
- **Execution** (`RecoverWallet`, same file): identity is proven with
  the exact same three factors and the exact same called functions as
  Branch A's `Recover` - `verifyAllSecurityAnswers`, `consumeValidOTP`,
  `verifyNewAddressOwnership` (all reused, not duplicated) - then a
  `swapOwner(prevOwner, oldSigner, newSigner)` call is submitted against
  the primary wallet's own Safe instead of a DB address update.
  `User.Address` never changes, only `User.SignerAddress`; no
  `GroupMember` row needs revoking, since every one of them references
  the wallet's `Address` (unchanged), exactly as PLAN.md §15.5 step 3
  anticipated. The authorizing signature is produced entirely
  server-side (`signAsRecoveryService`): since every recovery-operator
  key is itself `cryptoutil.DeriveKey`-derived (server-controlled, not a
  human's), enough of them to meet the recovery-service Safe's threshold
  personal_sign the nested EIP-1271 digest
  (`safe.MessageHashForSafe(recoveryServiceDomainSeparator,
  outerTxPreImage)`, the exact nested-ownership mechanic PLAN.md §13.4
  already established) synchronously, in one call, with no external
  round trip - unlike a `GroupMember`'s approval, which needs a human's
  signature over HTTP.
  - **Real bug found and fixed while wiring this up**:
    `RequestRecoveryOTP` (the OTP-issuing endpoint both branches share
    per §15.8) only checked `user.AccountRecoveryEnabled` before emailing
    a code - a wallet-recovery-only account (§15.2a's whole point is that
    a user may enable Branch B without Branch A) could never obtain an
    OTP to actually call `RecoverWallet` at all. Fixed to check
    `AccountRecoveryEnabled || WalletRecoveryEnabled`, caught by
    `TestRecoverWallet_Success_PreservesAddressAndSwapsSigner` failing
    with "no 6-digit OTP found in mail body" before the fix.
  - A `WalletRecoveryLog` row (new model, mirroring `AccountRecoveryLog`)
    is written in the same DB transaction as the `SignerAddress` update
    and OTP consumption, signed by the same recovery-authority key
    `buildRecoveryLog` uses (`RecoveryAuthoritySalt`), per §15.8's
    recommendation to reuse that tamper-evident attestation pattern.
- **Routes** (`internal/components/users/controllers/wallet_recovery.go`,
  new): `POST /v1/users/wallet-recovery/{enable,disable}` and their
  `/confirm` counterparts under the existing authed `/v1/users` group
  (§12's ordinary self-service model applies - the caller must already
  be the wallet's current signer); `POST
  /v1/account-recovery/:username/recover-wallet` alongside Branch A's
  existing `.../recover`, unauthenticated for the same reason that route
  is (§15.6 - the caller controls no currently-authorized key at all).
  `recovery.go`'s existing route and `Recover` function are completely
  untouched.
- **Tests**: `recovery_platform_test.go` (deterministic address
  computation, idempotent two-step deployment, failure propagation at
  each step) and `wallet_recovery_test.go` (full enable→disable
  round-trip with real personal_sign signatures from generated keys,
  wrong-signature rejection, fee-required-when-configured, and a full
  `RecoverWallet` round-trip asserting `Address` is preserved,
  `SignerAddress` is swapped, the OTP is single-use, and wrong security
  answers are rejected) - all against the existing `fakeBlockchain`,
  widened with `DeployContract`/`SafeNonce`/`SafeOwners`/
  `WaitForReceipt`/`BuildNativeTransferTx` fakes. Full module
  `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass;
  a real boot-smoke-test against SQLite confirmed every new route
  registers and Branch B correctly no-ops (logged, not fatal) when
  unconfigured. Consistent with Phase 9's own precedent, a live
  Base-Sepolia deployment of the recovery-service Safe/RecoveryGuard was
  **not** performed in this environment (this sandbox's egress policy
  blocks `sepolia.base.org`) - stated honestly rather than assumed to
  work.
- **Phase 5 (optional `DoInactiveAccountRecover`-equivalent) was not
  pursued**, per the roadmap's own framing of it as optional and worth
  confirming isn't redundant with Branch A first: Branch A already
  handles "no chain cost" identity recovery unconditionally, without the
  original's extra never-activated/fresh-account restriction, so a third
  mechanism would add real surface area (a third recovery entry point to
  reason about and secure) for a case Branch A already covers. Flagging
  this as a deliberate scope decision, not an oversight - revisit only if
  a concrete gap Branch A doesn't cover is identified.

Implementation does not begin until explicitly authorized.

## 16. Post-implementation documentation: testnet/mainnet, Swagger/OpenAPI, deployment config

Requested after §15 landed: clear instructions for running both this
service and the client apps against testnet and mainnet, a client-side
network switch if one didn't already exist, API-URL configuration
instructions per app, full Swagger/OpenAPI documentation, and a
`DEPLOYMENT.md` with realistic sample config values and sourcing
guidance for each. All delivered:

- **`DEPLOYMENT.md` gained a new §2 "Running against testnet vs.
  mainnet"**: explains that this service is one deployment per network
  (not a runtime-switchable server), how to get a dedicated Base RPC
  endpoint (Alchemy/Infura/QuickNode, with the exact dashboard steps),
  concrete `.env` values for each network, testnet faucet links, and the
  mainnet-specific cautions (never reuse a `*_KEY_SALT` across
  environments, verify curated token contract addresses against an
  authoritative source before seeding them).
- **`DEPLOYMENT.md` §4 (env vars) rewritten from a short "what must be
  set" table into a full reference**: every variable in `.env.example`,
  grouped by subsystem, each with a realistic sample value and - for
  anything sourced from a third party (Alchemy/Infura/QuickNode RPC keys,
  Sumsub/Dojah, Flutterwave, Stablerail, OneLiquidity, Discord webhooks,
  SMTP credentials) - exactly where to go get it.
- **Swagger/OpenAPI** (`internal/components/docs`, new): a hand-authored
  OpenAPI 3.0 spec (`controllers/assets/openapi.json`, embedded via
  `go:embed`) covering all ~150 routes across all 19 components, served
  at `GET /swagger/openapi.json`, plus an interactive Swagger UI at
  `GET /swagger/` (assets loaded from `cdn.jsdelivr.net`, needing the
  *browser's* internet access, not the server's - the spec itself is
  fully self-hosted). Hand-authored rather than swaggo/handler-comment
  generated: at this route count, per-handler annotation comments would
  be a large ongoing maintenance surface for marginal benefit over a
  spec written directly from reading each controller/model, and this
  codebase already keeps its authoritative documentation in PLAN.md
  rather than doc-comments-as-source-of-truth.
  - **Built via five parallel research/drafting agents**, one per
    cluster of components (users+sharedaccess+root+callbacks;
    assets+payments+swaps+rates; crypto+fiat+stablerail+kyc;
    market+patron+tokenization; announcements+reference+servicelinks+
    shortlink), each instructed to read the real controller/handler/model
    code rather than infer shapes from naming - then merged into one
    spec by a small Python script (validated: every `$ref` resolves, zero
    orphaned schemas, passes `openapi-spec-validator`'s full schema
    validation). Two agents independently caught and flagged that this
    codebase's real error envelope is `{"error": "<code>", "message":
    "<text>"}` (`internal/apperrors`), not the simpler placeholder shape
    the drafting instructions suggested - reconciled by documenting the
    real shape once in the spec's top-level description and adding a
    shared `ErrorResponse` schema, without going back to rewrite every
    per-operation example (a cosmetic inconsistency between an example
    and the documented canonical shape, not a correctness gap).
  - Verified live: a boot-smoke-test against SQLite confirmed
    `GET /swagger/openapi.json` (152 paths, 148 schemas) and
    `GET /swagger/` both serve correctly, and `GET /swagger` redirects to
    it.
- **`wallet-web` gained an in-app testnet/mainnet switch** (it had none -
  `VITE_WALLET_BACKEND_URL`/`VITE_CHAIN_ID` were single build-time-only
  values): see `wallet-web/PLAN.md` §10's new implementation note and
  `wallet-web/DEPLOYMENT.md` §2-4/§9 for the full design (paired
  `_TESTNET`/`_MAINNET` build-time vars, a `localStorage`-persisted
  runtime toggle in Settings, both origins allowlisted in the CSP either
  way). Verified: `npm run build:app-only` succeeds cleanly with the new
  `src/config/network.ts` module wired through `httpClient.ts`,
  `connectivityMonitor.ts`, and `authFlow.ts`.
- **`wallet-mobile` has no implementation yet** (still design-only per
  its own PLAN.md §13) - added a new §5.1 to `wallet-mobile/PLAN.md`
  designing the same switch (Flutter `--dart-define` build-time pairs +
  a `shared_preferences`-persisted runtime toggle) for whenever that
  build actually starts, rather than fabricating mobile app code that
  doesn't exist.
- **Noted, not fixed**: `wallet-web/PLAN.md` §11 already tracks (from
  before this work) that its API client still speaks the old SIWE +
  session-JWT scheme this service's own §12 replaced with per-request
  SignatureAuth - confirmed still true (`api/authFlow.ts`, `api/siwe.ts`,
  `api/authApi.ts`, and every API call site still thread a bearer
  `token`). The network switch changes *which* backend/chain
  `wallet-web` targets and is unaffected by this either way; actually
  fixing the auth mismatch is a separate, substantial rewrite (a generic
  per-request personal_sign path through the Worker, and updating every
  page's API calls) out of scope for this documentation-focused pass -
  left as the already-tracked §11 item, not silently patched over.
