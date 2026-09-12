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
| 2 | Account security & recovery (§4.3) | Phase 0 | **DONE** |
| 3 | KYC — Sumsub + Doja (§4.4) | Phase 0 | **DONE** |
| 4 | Fiat payments & activation — Flutterwave (§4.5) | Phase 0, benefits from Phase 3 (activation often gated on KYC) | **DONE** |
| 5 | Stablerail (§4.6) | Phase 3 (BVN/KYC-triggered onboarding) | **DONE** |
| 6 | Crypto deposit/withdrawal — OneLiquidity (§4.7) | §5's contract-deployment infra (for the mint side) | **DONE** (via treasury transfer, not mint — see §4.7) |
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
