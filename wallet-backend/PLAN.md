# wallet-backend — Base Blockchain Wallet Backend

> **Revision note:** the first version of this plan (and the code pushed
> alongside it) targeted the Stellar network. That was a misreading of
> "base" as a generic template name rather than the target chain: this
> service is meant to deploy to **Base**, Coinbase's Ethereum L2 (OP Stack).
> This revision replaces every Stellar-specific decision with its Base/EVM
> equivalent. **No code has been rewritten yet** — this document describes
> the rewrite and is waiting on approval before implementation starts.

## Purpose

A reusable starting point for a Base (EVM) blockchain wallet backend,
derived from `trovo-wallet-monorepo/backend` (the "Trovo Wallet API",
originally built on Stellar). It keeps the general-purpose architecture that
any wallet product needs and re-targets the blockchain-facing layers at
Base, while stripping out (or replacing with pluggable stubs) everything
specific to Trovo's business: real-world-asset tokenization, its
partner/merchant API, and its hard-wired third-party vendors.

Two decisions drive nearly every choice below, both stated explicitly
because they were under-specified in the first pass:

1. **Transaction generation is the core business logic, not the chain SDK.**
   The most valuable part of the original codebase isn't "it uses Stellar" —
   it's the discipline around building a correct, replay-safe, non-custodial
   transaction (right account state, right fee/sequencing, right
   calldata/asset encoding) and handing it off for signature. That discipline
   is what gets ported and preserved; only the concrete chain calls
   underneath it change. Every component below is organized around a
   **Build → Sign → Submit** pipeline for exactly this reason, and that
   pipeline's shape does not change between Stellar and Base — only what's
   inside `Build` and `Submit` does.
2. **The client app must be able to work offline.** Signing is the one step
   that must never require connectivity: a device can build a transaction
   while online, go offline to sign it (hardware wallet, secure enclave, or
   simply airplane mode), and submit it once it reconnects — possibly much
   later, possibly out of order with other queued actions. §3 below makes
   this a first-class API design constraint, not an afterthought.

## 1. Why Base changes the design (and what stays the same)

Base is an OP-Stack Ethereum L2: it speaks standard Ethereum JSON-RPC, uses
secp256k1/ECDSA keys and 0x-addresses, and settles standard EVM
transactions. Concretely, this means:

- **No bespoke chain SDK is needed at all.** Stellar required
  `github.com/stellar/go-stellar-sdk` for horizon clients, XDR encoding,
  keypair types, etc. Base needs none of that — `github.com/ethereum/go-ethereum`
  (the standard, canonical Ethereum client library, "geth") talks to Base's
  RPC endpoint like it would talk to any Ethereum node. This is a
  simplification, not a lateral move: fewer moving parts, and zero
  dependency on any single chain's proprietary tooling.
- **Assets are ERC-20 contracts, not issuer/trustline pairs.** There is no
  Stellar-style "open a trustline before you can hold an asset" step in the
  EVM model — anyone can hold any ERC-20 token balance with no
  authorization step. The one authorization concept that *does* carry over
  is **allowances** (`approve`/`transferFrom`), which is the closest EVM
  analog to a trustline and is where the "ephemeral" pattern in §5 re-applies.
- **Fees are gas, not a fixed base reserve.** Transactions specify
  `maxFeePerGas`/`maxPriorityFeePerGas` (EIP-1559) instead of Stellar's flat
  per-operation fee; the `Build` step now includes a gas estimate.
- **Everything not chain-specific is unaffected.** The DI pattern,
  component-per-vertical-slice layout, error shape, DB migration harness,
  cache abstraction, and every pluggable integration interface
  (`notify`, `kyc`, `fiat`, `rates`, `alerting`, `storage`) from the first
  plan are chain-agnostic and carry over untouched — see §6.

## 2. What must be rewritten for Base/EVM

| Area | Was (Stellar) | Becomes (Base/EVM) |
|---|---|---|
| Blockchain client | `internal/network`: Horizon client (`horizonclient.Client`), `txnbuild` transaction assembly | `internal/network`: `ethclient.Client` pointed at a Base RPC endpoint; transactions assembled with `go-ethereum/core/types` (`types.NewTx`, `types.DynamicFeeTx` for EIP-1559) |
| Wallet identity | Stellar keypair, `G...` public address (ed25519) | EVM account, `0x...` address (secp256k1); `crypto.PubkeyToAddress` |
| Server-controlled signer derivation | `cryptoutil.DeriveKeypair`: `SHA256(seed) → ed25519.FromRawSeed` | `cryptoutil.DeriveKey`: BIP-32/BIP-44-style HD derivation (or HKDF) → secp256k1 private key via `go-ethereum/crypto`. Raw SHA-256 output isn't a safe substitute for HD derivation on secp256k1 the way it was for ed25519 — use a real derivation path, not a shortcut. |
| Primary API auth | Custom header scheme: client signs `publicKey+timestamp` with its Stellar key; verified via `keypair.Verify` | **SIWE — Sign-In With Ethereum (EIP-4361)**: server issues a one-time nonce, client signs a structured SIWE message with `personal_sign` (EIP-191), server recovers the address via `crypto.Ecrecover`/`SigToPub` and verifies it matches, then issues a short-lived **JWT session** (reusing the JWT infrastructure already built for the admin surface) for subsequent requests. This fixes a real gap in the original custom scheme (a captured signed header set was replayable against any endpoint in the timestamp window) and is the recognized, wallet-interoperable standard for EVM chains — every major EVM wallet already knows how to produce a SIWE signature. |
| Format validation | Stellar strkey (`keypair.ParseAddress`) | EIP-55 checksummed address validation (`common.IsHexAddress` + checksum comparison) |
| Users model | `PublicKey` (Stellar `G...`, 56 chars) | `Address` (EVM `0x...`, 42 chars, EIP-55 checksummed) |
| Assets component | Curated asset catalog (Stellar `CreditAsset{Code, Issuer}`) + trustline build/submit | Curated **token** catalog (`{Symbol, ContractAddress, Decimals}`) + ERC-20 balance lookup (`balanceOf`) + **allowance build/submit** in place of trustline build/submit (see §5) |
| Payments component | `txnbuild.Payment` (native XLM or `CreditAsset`) | Native ETH transfer (`to`, `value`) or ERC-20 `transfer(to, amount)` calldata, ABI-encoded via `accounts/abi` |
| Swaps component | `txnbuild.PathPaymentStrictSend` against Stellar's built-in DEX | A generic, **router-address-configurable** contract-call builder (ABI-encode a call to whatever DEX router is configured — Uniswap V3 `SwapRouter02` and Aerodrome are both live on Base) rather than hardcoding one DEX, in keeping with "base template, not one product's opinion" |
| Root health/discovery | SEP-1 `stellar.toml` | Plain health JSON reporting `chainId` + RPC URL; no EVM equivalent of SEP-1 is needed since token discovery is just reading a contract |

## 3. Offline-capable API design (new, mandatory across every mutating endpoint)

Every action that changes on-chain state keeps the **Build → Sign → Submit**
split from the first plan, but three additions make it actually work for an
app that goes offline between steps:

1. **`Build` returns everything needed to sign completely offline.** Nonce,
   gas parameters (`maxFeePerGas`/`maxPriorityFeePerGas`/`gasLimit`),
   `chainId`, `to`, `value`, and `data` are all resolved server-side and
   returned in one response. Once a device has this payload, signing needs
   no further network access — the private key can live in an air-gapped
   signer, a hardware wallet, or just a phone in airplane mode.
2. **`Submit` is idempotent.** The caller supplies a client-generated
   idempotency key (a UUID minted at `Build` time and threaded through to
   `Submit`); resubmitting the same signed transaction after a flaky
   reconnect returns the original result instead of erroring or double-processing.
   This matters specifically because an offline app's natural retry
   behavior on reconnect is "try sending again."
3. **Nonce handling supports offline batching.** An app that builds several
   transactions while offline (e.g., queued while a user has no signal) needs
   sequential nonces reserved in advance, since EVM transactions from one
   address must execute in strict nonce order. `Build` accepts an optional
   explicit nonce (falling back to "next known nonce" when omitted) so a
   client can pre-build a batch of N transactions offline-ready, sign all N
   offline, and submit them in order once connectivity returns without a
   round trip between each one.

None of this changes the non-custodial guarantee: the server never sees,
stores, or requests a user's private key at any point in this flow — offline
signing is a stronger version of the same guarantee, since it means the key
never has to touch a networked device at the moment of signing.

## 4. What gets dropped entirely (unchanged from the first plan)

Still business-specific to Trovo, still out of scope for a base wallet,
independent of which chain it targets:

- **Tokenization subsystem** (`tokenization.go`, ~6800 lines; primary/secondary
  sale, early exit, payout-schedule engine, minting approvals).
- **Patron/membership subscriptions** — a SaaS-tier feature unrelated to wallets.
- **Servicelinks partner API** (`/v1/trovo-api/...`) — Trovo-specific B2B surface.
- **Stablerail-specific models/services** (BVN onboarding, NGN on/off-ramp polling).
- Trovo-branded constants and assets, hardcoded Discord webhook URLs.
- CockroachDB side-store — one Postgres instance covers `PaymentHistory`.
- DigitalOcean Container Registry + Portainer-webhook deploy step.

## 5. Reusable design-pattern equivalents

The three Stellar-specific patterns worth preserving from the first plan
still apply — two carry over exactly, one needs an EVM-native substitute:

1. **Ephemeral approval, not ephemeral trustline.** The original pattern —
   open a trustline, use it inside one atomic transaction, close it in the
   same transaction so nothing lingers — has no direct Stellar-trustline
   equivalent in the EVM model, but the same *intent* (never leave a
   standing permission wider than the single operation that needed it) maps
   onto ERC-20 allowances: `approve(exact amount)` immediately followed by
   the contract call that consumes it via `transferFrom`, with the caller
   encouraged to `approve(0)` afterward rather than leaving a lingering
   allowance. This is weaker than Stellar's atomic guarantee (two
   transactions instead of one, since `approve`+`transferFrom` can't be
   combined for an EOA without a smart contract wallet), which is worth
   documenting plainly rather than glossing over. Where a project can use a
   smart contract wallet (see the ERC-4337 note in §9), a single
   `execute batch` call restores the original atomicity.
2. **Decoupled build/pay/submit for fiat purchases — unchanged.** Pre-build
   (and where applicable pre-sign) the on-chain leg before the fiat charge
   starts; the fiat webhook triggers final submission; one client-supplied
   idempotency ID threads through all three phases. This pattern was never
   Stellar-specific and needs no modification.
3. **Config-driven settlement asset — unchanged, now an ERC-20 address.**
   Any settlement/quote token (e.g. USDC on Base) must be a per-deployment
   config value, never a literal contract address baked into business
   logic — same principle as before, just a `0x...` value instead of a
   Stellar asset code.
4. **New: nonce management replaces "channel accounts."** Stellar's channel-account
   pool existed to avoid sequence-number contention when a server signs many
   transactions from shared signer roles. The EVM equivalent problem is nonce
   contention under concurrent submission from the same address; the fix is
   a per-address nonce-tracking service (in-memory counter seeded from the
   chain, incremented under a mutex per signer) rather than a pool of
   alternate accounts. See §9 for a further-out alternative (ERC-4337
   relayers/paymasters) that sidesteps this problem entirely for smart
   accounts.

## 6. What still carries over as-is (chain-agnostic)

Everything in this list is already correctly designed for any chain and
needs no change for the Base rewrite:

| Area | Notes |
|---|---|
| Boot sequence & DI pattern (`main.go`, `GlobalConfig`) | Unaffected by chain choice. |
| Layered component structure | `controllers/services/models` per component — unaffected. |
| Error shape (`internal/apperrors`) | Unaffected. |
| DB migration harness (`internal/db`) | Unaffected — same Postgres/SQLite split. |
| Cache abstraction (`internal/cache`) | Unaffected. |
| JWT admin auth (`internal/middleware`) | Unaffected in its own right — and now reused for primary-API sessions after SIWE, see §2. |
| `notify` (mail/SMS/push), `storage`, `kyc`, `fiat`, `rates`, `alerting` | All chain-agnostic interfaces with free/local default implementations — no change. |
| `announcements`, `root` (minus SEP-1), `callbacks` components | Business logic here never touched Stellar specifics. |

## 7. Target module layout (revised)

```
wallet-backend/
├── main.go
├── go.mod
├── Dockerfile
├── docker-compose.yml            # postgres + redis
├── .env.example                  # now includes BASE_RPC_URL, BASE_CHAIN_ID
├── Makefile
├── .golangci.yml
├── .github/workflows/ci.yml
├── README.md
├── internal/
│   ├── sharedconfig/             # GlobalConfig + Env loader
│   ├── db/                       # unchanged
│   ├── cache/                    # unchanged
│   ├── network/                  # REWRITE: ethclient wrapper — the only package importing go-ethereum
│   ├── middleware/                # REWRITE: SIWE auth (replaces Stellar-signature auth) + JWT (unchanged)
│   ├── cryptoutil/                # REWRITE: secp256k1 HD derivation, AES-GCM/bcrypt unchanged
│   ├── apperrors/                 # unchanged
│   ├── validators/                # REWRITE: EVM address validation
│   ├── notify/ kyc/ fiat/ rates/ alerting/ storage/   # unchanged
│   └── components/
│       ├── root/                 # REWRITE (drop SEP-1, report chainId)
│       ├── users/                 # REWRITE (Address instead of PublicKey)
│       ├── assets/                # REWRITE (ERC-20 catalog + allowance build/submit)
│       ├── payments/               # REWRITE (native/ERC-20 transfer build/submit)
│       ├── swaps/                  # REWRITE (generic DEX-router call builder)
│       ├── announcements/          # unchanged
│       └── callbacks/              # unchanged
└── docs/
```

## 8. Base network configuration

| | Chain ID | RPC (public default) |
|---|---|---|
| Base Mainnet | 8453 | `https://mainnet.base.org` |
| Base Sepolia (testnet) | 84532 | `https://sepolia.base.org` |

Both are standard OP-Stack/Ethereum JSON-RPC — no Base-specific client
library exists or is needed; `go-ethereum`'s `ethclient.Dial(rpcURL)` works
against either unmodified. Native gas token is ETH; USDC (Circle-native on
Base) is the natural default example for a config-driven settlement asset
(§5.3), kept as a config value, never hardcoded.

## 9. Suggested rewrite order

1. `internal/cryptoutil` (secp256k1 key derivation, EIP-191 message hashing) + `internal/validators` (EVM address checks) — no network dependency, quick to get right and unblocks everything else.
2. `internal/network` — `ethclient` wrapper: account/nonce/balance lookup, `BuildNativeTransferTx`, `BuildERC20TransferTx`, `BuildApproveTx`, a generic `BuildContractCallTx` for swaps, gas estimation, `SubmitRawTransaction`. Validate against Base Sepolia early with a real funded testnet key.
3. `internal/middleware` — SIWE nonce issuance (stored in `internal/cache`) + signature verification + JWT session issuance.
4. `db`/model updates: `users.Address`, `assets` token catalog fields, `payments` history schema (mostly unchanged shape, new field names).
5. Rewrite `users`, `assets`, `payments`, `swaps` components against the new `network` layer, preserving the offline-capable Build/Submit contract from §3.
6. Touch `root` (health reports `chainId` instead of `NetworkPassphrase`); confirm `announcements`/`callbacks` need no change.
7. Update `Dockerfile`/`docker-compose.yml`/`.env.example`/`README.md`/CI: remove every Stellar reference, add `BASE_RPC_URL`/`BASE_CHAIN_ID`.
8. Full `go build`/`vet`/`test` pass + a live smoke test against Base Sepolia (register a user with a testnet address, build+sign+submit a native transfer, confirm on a Base Sepolia block explorer).

## 10. Open decisions for whoever implements this

- **Ethereum client library**: `github.com/ethereum/go-ethereum` (the
  canonical, first-party Go client) is recommended over a lighter
  JSON-RPC-only wrapper — it also brings ABI binding codegen (`abigen`) for
  free if the project later needs typed contract bindings.
- **SIWE implementation**: hand-rolling EIP-4361's nonce+message format is
  small but easy to get subtly wrong; recommend a reference library (e.g.
  `github.com/spruceid/siwe-go`) over custom parsing, keeping only the
  nonce-storage and JWT-issuance glue as project-specific code.
- **Default DEX router for swaps**: deliberately left config-driven rather
  than picked for the team — document Uniswap V3 `SwapRouter02` and
  Aerodrome as the two live options on Base, but don't hardcode either.
- **Smart accounts (ERC-4337) — out of scope for v1, flagged for v2.** Base
  has strong first-party support for account abstraction (Coinbase Smart
  Wallet, a Base-run paymaster for gas sponsorship), which would let a
  future version replace raw EOA-signed transactions with sponsored
  `UserOperation`s and restore true atomicity to the approval pattern in
  §5.1. That's meaningfully more infrastructure (bundlers, paymasters,
  `UserOperation` plumbing) than a base template should assume on day one,
  but it's the natural next step once the EOA-based flow is proven, and is
  worth calling out precisely because it's a Base-native capability rather
  than a generic EVM one.
- **JWT library**: `golang-jwt/jwt/v5`, unchanged from the first plan.
- **SQLite driver**: `glebarez/sqlite` (pure Go, no CGO), unchanged from the
  first plan — this was never Stellar-related.
