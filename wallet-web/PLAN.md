# wallet-web — Non-Custodial Web Wallet for the Base Port

**Plan only — no implementation until explicitly authorized.**

## 0. Purpose

A browser-based, non-custodial wallet app for `wallet-backend` (the Base
port of the Trovo wallet API) and its `wallet-payment-history-engine`.
Visually and structurally themed to match `trovo-wallet-monorepo/web` (the
original Stellar-era wallet web app), but with its wallet-holding core
rebuilt from scratch: a WASM module (`wallet-core`) that is the only place
mnemonics and private keys ever exist in cleartext, backed by encrypted
browser storage, running in its own isolated execution context.

Two mnemonics are imported, not one:

- **Signer** — the key the user authenticates with (SIWE sign-in,
  `wallet-backend`'s session identity).
- **Primary wallet** — the address whose balances, curated-token holdings,
  and indexed payment history (via `wallet-payment-history-engine`) the
  app displays, and on whose behalf payments/swaps are built and signed.

In the common case these are the same mnemonic (self-custody, one wallet
— exactly what `wallet-backend`'s registration flow assumes today). The
app also supports them being two *different* mnemonics, which requires a
small `wallet-backend` contract change identified in §6 below.

**Connectivity model**: unlocking the local vault and viewing
already-known wallet data (addresses, last-fetched balances/history) both
work fully offline — neither touches `wallet-backend`. Every
transaction (payment or swap, from building the unsigned tx through
submitting the signed one) requires a live, verified connection and is
not offered at all while offline. §6.4 covers the connectivity-status
architecture and the online/offline indicator this requires.

## 1. Audit: what already exists to build on and what not to repeat

### 1.1 Theme source: `trovo-wallet-monorepo/web`

A Vite + React 18 + TypeScript + Redux Toolkit + `react-router-dom` +
Tailwind + `tw-elements-react` SPA. The visual system this plan ports
verbatim:

- **Colors** (`tailwind.config.js`): `primary` scale
  `100 #F2F6F9 → 200 #CCDBE7 → 300/500 #99B6CF → 600 #6692B8 → 700
  #336DA0 → 800 #004988`, and an accent `trovored`
  (`light #FFE7E2`, `primary #BE3800`).
- **Fonts**: `MatahariRegular`/`MatahariExtended` for display/headings
  (`public/fonts/Matahari-400Regular.ttf`,
  `MatahariExtended-600ExtSemBd.ttf`), `Montserrat`
  Regular/Medium/SemiBold for body/UI text.
- **Layout convention**: split-screen auth pages — a left brand panel
  (`bg-primary-100`, logo + illustration, e.g.
  `src/pages/importWallet/importWallet.tsx`) beside a right-hand form
  panel; primary actions are `bg-primary-800 rounded-lg` buttons
  (`src/components/button.tsx`); inputs are `ring-1/2 ring-gray-200
  focus-within:ring-primary-600 rounded-md` (`src/components/textInput.tsx`);
  field labels are `text-primary-700`.
- **Brand**: logo at `/images/trovoLogo.png`, wordmark "Trovo App"
  (`src/components/trovoBrand.tsx`).

`wallet-web` copies the Tailwind config, font files, and the small
reusable component set (`Button`, `ButtonSecondary`, `TextInput`,
`TrovoBrand`-equivalent) verbatim, then builds new pages against them —
not a fork of the old app's routes or Redux slices.

### 1.2 What the original app's wallet-holding code got right and wrong

`trovo-wallet-monorepo/web/src/utils/encryptor.ts` and
`trovoSDK.ts` are the only precedent in this codebase for "hold a wallet
secret in a browser app," and are read closely here because this plan's
entire job is to replace them with something stronger:

**Worth keeping the intent of:**
- Password-derived AES-256-GCM encryption of secret material via
  WebCrypto (`Encryptor.deriveKeyFromPassword`/`encryptData`/`decryptData`,
  `encryptor.ts:8-66`) — the right primitive family, just under-tuned
  (below).
- A distinct "signer" vs. wallet-address concept already existed in the
  original Stellar data model (`trovo-wallet-monorepo/backend/internal/
  components/users/models/user.go:21,62`: `User.PrimarySigner` vs.
  `UserWallet.Signer` — *"if ID is same as signer, then it is a primary
  wallet"*). This plan's two-mnemonic import is a direct continuation of
  that idea, re-grounded in the EVM data model (§4).

**Bugs/weaknesses this plan does not reproduce:**
1. **PBKDF2 with 10,000 iterations** (`encryptor.ts:22`) is far below
   current guidance (OWASP recommends ≥600,000 for PBKDF2-SHA256, or a
   memory-hard KDF instead). §5.3 uses Argon2id.
2. **The KDF "salt" is derived from public information** — a hash of the
   username, or the wallet's own public key (`encryptor.ts:33,59,77-82,
   96-101`) — not a per-encryption random value. A salt's job is
   uniqueness, not secrecy, but a salt anyone can recompute from public
   data (a username, a public key) buys none of the rainbow-table
   resistance a random salt provides. §5.3 uses a random salt stored
   alongside the ciphertext.
3. **Decrypted user data (including, transitively, the path to the secret
   key) flows into Redux state** (`encryptor.ts:102-109,122-127`, dispatched
   as `setUser`/`setTempUser`). Redux DevTools (installed by default in
   most developer browsers, and not reliably absent in a compromised
   user's browser either) can inspect the entire state tree, and Redux
   state has no secrecy boundary from any script running on the page —
   including an XSS payload. §5.1/§6.2 draw a hard line: no key material,
   decrypted or encrypted, ever enters Redux, React state, `localStorage`,
   or any other main-thread-readable store.
4. **A layered, home-grown key hierarchy** — the "password" itself is
   stored encrypted, decryptable via a hash of the username
   (`encryptor.ts:75-83`) — adds complexity without adding security,
   since a username is not a secret. §5.3's vault format is a single
   Argon2id-derived key straight from the user's real password.
5. **No visible session/idle lock** — decrypted state, once set, has no
   automatic re-lock path in the reviewed code. `react-idle-timer` is
   already a dependency of the original app but the reviewed flows don't
   wire it to a wallet lock. §5.4 makes idle-lock a core, non-optional
   behavior.

## 2. `wallet-backend` integration surface (audited)

Confirmed routes this app talks to (`wallet-backend/internal/components/
*/controllers/controllers.go`):

| Endpoint | Purpose |
|---|---|
| `GET /auth/nonce` | fetch a single-use SIWE nonce |
| `POST /auth/verify` | exchange a signed SIWE message for a session JWT |
| `POST /users` (authed) | register (creates `User` + a `UserWallet{IsPrimary:true}` row — see §4) |
| `GET /users/:username` | public profile lookup |
| `POST /users/security-answers`, `/security-answers/verify`, `/account-recovery` | account recovery setup |
| `POST /account-recovery/:username/request-otp`, `/recover` | account recovery execution |
| `GET /assets`, `/assets/balance/:address` | curated token list + balances |
| `POST /payments/build` | server returns an **unsigned** `network.UnsignedTx` (chainId, nonce, to, value, data, gas, EIP-1559 fees, `type:"0x2"`) |
| `POST /payments/submit` | client submits a **signed** raw tx string back |
| `GET /payments/history/:address` | reads `wallet-payment-history-engine`'s indexed history (once that engine's Phase 9 coordination note, tracked in its own `PLAN.md`, is done — until then, `wallet-backend`'s own narrower submit-time history) |
| `POST /swaps/approve/build`, `/swaps/approve/submit` | same build/sign/submit split, for ERC-20 approvals ahead of a swap |
| `POST /sharedaccess/groups`, `GET .../groups/:groupId`, `.../balance/:groupId`, `POST .../actions`, `GET .../actions[/:actionId]`, `POST .../actions/:actionId/approve|reject` | multi-party wallet groups (out of scope for `wallet-web` v1, see §8) |

**This confirms the core non-custodial mechanism this plan needs already
exists server-side**: `wallet-backend` never asks for a private key or
mnemonic anywhere in its API. Every state-changing action is a
build-unsigned/sign-locally/submit-signed round trip. `wallet-web`'s only
new job is to be a correct, hardened place to do the "sign locally" step
and to hold the key material that step needs.

## 3. Signer and Primary Wallet — corrected: one mnemonic, not two

**This section is rewritten from an earlier draft, which proposed a
`link-primary` endpoint and a genuinely-separate importable "primary
wallet" mnemonic. That design assumed the primary wallet was, like the
signer, a bare EOA with its own private key someone could hold and sign
with directly. `wallet-backend/PLAN.md` §13 (the Base sub-wallet/
shared-access redesign, extended to cover wallet recovery in §15) makes
the primary wallet a Safe smart-contract account instead - specifically
so a lost signer key can be replaced without losing the wallet, the way
the original's own Stellar-native multisig allows. A Safe has no private
key of its own. There is nothing to import, and nothing for
`sign_link_primary_message` to prove control of - so that whole
mechanism is withdrawn, not merely deferred.**

The corrected model has exactly **one** mnemonic/key in the whole system:
the **signer**. `wallet-backend`'s registration flow derives the signer
EOA from it, then computes (CREATE2, off-chain, no chain call) a Safe
address with `owners: [signerEOA], threshold: 1` - that computed address
*is* `User.Address`, the permanent primary-wallet identity
(`wallet-backend/PLAN.md` §13.3/§13.4). The signer key is never
"the primary wallet's key" in the sense of directly holding the funds'
private key; it's the key that currently *operates* the primary wallet's
Safe, and - per §15's recovery design - can be swapped out later without
`User.Address` ever changing.

Practical consequences for this app:

- `wallet-core` only ever needs to hold **one** role's key, not two - see
  §4.2's corrected API surface below.
- Signing a payment/swap sourced from the primary wallet no longer means
  "sign a raw EIP-1559 transaction with the primary wallet's key" (there
  is no such key) - it means building and EIP-712-signing a **Safe
  transaction** with the signer's key, then submitting it (with that
  signature) for `wallet-backend` to relay as the Safe's
  `execTransaction` call. §6.3 below is updated accordingly, and this is
  the same EIP-712 signing capability §12 already tracks as a
  `wallet-core` addition - it turns out to be needed for *every*
  primary-wallet transaction now, not only shared-access approvals as
  originally scoped there.
- **A genuinely different, smaller feature is still worth keeping in
  mind for later, without conflating it with "primary wallet"**: letting
  a user point this app at an *external*, already-existing address purely
  to view its balance/history (read-only "watch an address"), with no
  claim of control and nothing to sign. If wanted, that's a separate,
  optional feature to design when asked for - not a substitute for, or a
  revival of, the withdrawn two-mnemonic model.
- **This is real rework, not just a documentation fix.** §9's "Phased
  build roadmap" and §10's "Implementation notes" below are left as an
  accurate historical record of what Phases 1-11 actually built - a
  two-mnemonic import flow, a `link-primary` client stub, and
  `sign_transaction`-only payment/swap signing - all under the
  since-corrected assumption. None of that is wrong to have built at the
  time; it's what needs a follow-up implementation pass (import flow,
  `POST /users/wallets/link-primary` client code, and the payment/swap
  signing flow itself per §6.3) once `wallet-backend`'s §13 redesign is
  authorized and this app's own rework is scheduled - not a surprise to
  discover mid-migration.

## 4. `wallet-core`: a WASM module as the sole key-holding boundary

### 4.1 Why WASM, and why a Worker

A dedicated Rust crate compiled to WebAssembly, run inside a **Web
Worker**, not the page's main thread. This is the load-bearing security
decision in this plan, so it is worth stating precisely what it buys and
what it does not:

- **What it buys**: the main thread — where React renders, where a
  dependency's or a reflected-XSS payload's JavaScript would run — has no
  direct memory access into a Worker's separate global scope. It can only
  ask the worker to do something via a narrow, typed `postMessage` RPC
  (§4.2) and receive back an address, a signature, or a signed
  transaction — never a mnemonic or private key. This is the same
  isolation principle browser-extension wallets (MetaMask et al.) use for
  their background/keyring context, applied here without needing a
  browser-extension host.
- **What it does not buy**: a script capable of driving the RPC itself
  (i.e., already running with the page's privileges) can still ask the
  worker to sign an attacker-chosen transaction while the vault is
  unlocked — Worker isolation defeats *memory scraping*, not a fully
  page-privileged attacker abusing the legitimate API surface. That
  residual risk is addressed by minimizing what the main thread can ask
  for (§4.2's API is deliberately narrow) and by strict CSP (§7.3), not
  by the Worker boundary alone.
- **Why WASM specifically, not just isolate the same logic as plain JS in
  a Worker**: `zeroize`-backed Rust structs give a real, language-enforced
  guarantee that key material is overwritten in memory when it goes out
  of scope — JavaScript strings/typed arrays have no such guarantee (a
  string literal's backing bytes can persist in the JS engine's memory
  arena well after the last reference is dropped, pending GC, and GC
  timing is not controllable). Rust's `Drop` runs deterministically.
  WASM also keeps every cryptographic primitive (KDF, AEAD, ECDSA,
  Keccak) in one small, auditable, dependency-pinned artifact instead of
  several independent JS crypto packages, each its own supply-chain
  surface.

### 4.2 Exposed API (the *entire* main-thread-facing surface)

Via `wasm-bindgen`, called only from the Worker's own message handler —
the main thread never imports the WASM module directly:

```
generate_mnemonic(word_count: 12 | 24) -> String
validate_mnemonic(phrase: &str) -> bool
derive_preview_address(phrase: &str) -> String   // BIP-44 m/44'/60'/0'/0/0 — see §4.4
encrypt_and_store(role: "signer", phrase: &str, password: &str) -> VaultRecord
unlock(role, password: &str) -> UnlockResult { address }   // decrypts into worker-local memory only
lock(role)                                                  // zeroizes the in-memory key immediately
sign_siwe_message(role: "signer", message: &str) -> HexSignature
sign_transaction(role: "signer", unsignedTxJson: &str) -> HexRawSignedTx
sign_typed_data(role: "signer", typedDataJson: &str) -> HexSignature   // EIP-712 — needed for every primary-wallet Safe transaction now (§3, §6.3), tracked as a real gap in §12
wipe(role)                                                  // deletes that role's vault record entirely (remove wallet)
```

Corrected from an earlier draft: `role` only ever takes one value,
`"signer"` - §3 above explains why the `"primary"` role and
`sign_link_primary_message` (which existed solely to prove control of a
primary-wallet private key that no longer exists) are withdrawn rather
than kept as unused options. `sign_typed_data` is new relative to the
originally-built `signing.rs` (§12) and is what actually authorizes a
primary-wallet payment/swap now, in place of `sign_transaction` alone.

No function returns a mnemonic or raw private key once `encrypt_and_store`
has completed for that role — from that point on, the only way back to
the key is `unlock` inside the worker, and the only things that leave the
worker are addresses and signatures. `derive_preview_address` is the one
function that handles a raw phrase after initial entry, used to show the
user "this mnemonic controls address 0x..." *before* they commit to
encrypting and storing it, and to verify a re-entered recovery phrase
matches the wallet it claims to.

### 4.3 Crates (requirements, not a version-pinned lockfile — resolved at implementation time)

- `wasm-bindgen`, `js-sys` — JS/WASM boundary.
- `bip39` — mnemonic generation/validation/seed derivation.
- A BIP-32/44 HD derivation crate compatible with secp256k1 (e.g.
  `coins-bip32`) — must support the standard Ethereum path (§4.4).
- `k256` — secp256k1 ECDSA signing (recoverable signatures, `(r,s,v)`).
- `sha3` — Keccak-256, for EVM address derivation and EIP-1559 transaction
  hashing.
- An EIP-1559 (type `0x02`) transaction RLP encoder matching what
  `wallet-backend`'s `network.UnsignedTx` (`chainId, nonce, to, value,
  data, gas, maxFeePerGas, maxPriorityFeePerGas`) already produces —
  either a small hand-rolled encoder (kept auditable and dependency-free)
  or a wasm-compatible subset of an existing crate, decided at
  implementation time based on binary size and audit surface trade-offs.
- `argon2` (RustCrypto) — vault key derivation (§4.3/§5.3... see §5).
- `aes-gcm` (RustCrypto) — vault encryption.
- `zeroize` — deterministic scrubbing of every struct that ever holds a
  mnemonic, seed, or private key.
- `getrandom` with the `wasm_js` backend — routes all randomness (mnemonic
  entropy, AES nonces, Argon2 salts) through the browser's
  `crypto.getRandomValues`, never a non-CSPRNG fallback.

### 4.4 Derivation path is non-negotiable: standard Ethereum BIP-44

`m/44'/60'/0'/0/0` — the path MetaMask, Coinbase Wallet, Rabby, and every
mainstream EVM wallet use for the first account. A user importing a
mnemonic they already use elsewhere must see the *same* address here that
they see in their existing wallet, or the app is silently showing them
the wrong account — a correctness requirement, not a style choice. No
custom or non-standard path is acceptable for v1.

## 5. Encrypted storage

### 5.1 What is stored, and where

**IndexedDB** (not `localStorage` — structured, larger quota, and
critically, `localStorage` is synchronously readable by any same-origin
script including a same-origin XSS payload with no `await` needed, while
IndexedDB access is at least async and easier to keep entirely
worker-side): one record for the `signer` role (§3 - no `primary` role
exists to store, since the primary wallet has no private key of its
own), written and read *only* from within the Worker (§4.1) — the main
thread never touches IndexedDB for vault data directly, only asks the
worker to.

Each `VaultRecord`:

```
{
  role: "signer",
  address: string,              // derived address, safe to expose - not secret
  kdf: "argon2id",
  kdfParams: { memoryKiB, iterations, parallelism },  // tuned per §5.2
  salt: base64,                 // random per record, not derived from any public value (fixes §1.2 finding 2)
  nonce: base64,                // AES-GCM nonce, random per encryption
  ciphertext: base64,           // Argon2id(password, salt) --AES-256-GCM--> encrypted mnemonic
  createdAt: timestamp
}
```

Only ciphertext plus the public parameters needed to reproduce the KDF
touch disk. The address field is intentionally plaintext (not secret) so
the UI can show "Primary wallet: 0xabc…" without unlocking anything.

### 5.2 KDF tuning: Argon2id, off the main thread

Argon2id (memory-hard, GPU/ASIC-resistant, current OWASP-recommended
default) replaces PBKDF2 (fixes §1.2 finding 1). Starting parameters —
tuned during implementation against real target devices, not guessed
here — follow OWASP's Argon2id baseline (e.g. ~19 MiB memory, 2
iterations, 1 degree of parallelism) as a floor, adjusted upward if
low-end mobile unlock latency proves acceptable at a higher cost.
Running this inside the Worker (§4.1) is what makes a deliberately slow
KDF acceptable UX at all — it cannot freeze the page's UI thread.

### 5.3 Session lifecycle and idle lock

Fixes §1.2 finding 5 (no enforced idle lock in the original):

- **Unlock** decrypts a role's mnemonic into worker-local memory only,
  for as long as the session is active. No plaintext or decrypted key
  ever crosses back to the main thread.
- **Idle timeout** (`react-idle-timer`, already proven in the original
  app — reused here for its intended purpose this time) triggers `lock`
  on both roles after a configurable inactivity window, zeroizing
  worker-side key memory immediately.
- **Multi-tab consistency**: a `BroadcastChannel` (or a `storage` event on
  a non-secret "lock heartbeat" key) propagates a lock event to every
  open tab sharing the origin, so locking in one tab locks the wallet
  everywhere, not just where the idle timer fired.
- **Explicit lock** is always one click away, and every tab starts locked
  on load — unlocking is always a deliberate, password-gated action, never
  implied by a page refresh restoring prior state.
- **Failed-unlock throttling**: exponential backoff after repeated wrong
  passwords (tracked per role, in memory, reset on success), to slow
  brute-forcing of a stolen/leaked encrypted vault export without adding
  a false sense of security — Argon2id's own cost is the real defense;
  this just removes the "just script 10,000 guesses instantly" shortcut
  for an attacker running inside the page.

## 6. Frontend application architecture

### 6.1 Module layout

```
wallet-web/
├── PLAN.md
├── wallet-core/                    # Rust crate → WASM (§4)
│   ├── Cargo.toml
│   └── src/
│       ├── lib.rs                  # wasm-bindgen exports (§4.2) — the ONLY public surface
│       ├── mnemonic.rs             # bip39 generate/validate, BIP-44 derivation (§4.4)
│       ├── vault.rs                # Argon2id + AES-256-GCM encrypt/decrypt, zeroize (§5)
│       └── signing.rs              # EIP-191 personal_sign (SIWE) + EIP-1559 tx signing
└── app/                            # Vite + React 18 + TS SPA
    ├── index.html
    ├── package.json
    ├── tailwind.config.js          # ported primary/trovored palette + font families (§1.1)
    ├── public/fonts/                # ported Matahari/Montserrat files
    └── src/
        ├── worker/
        │   └── walletCoreWorker.ts # loads the WASM module, owns the message-RPC handler (§4.1/§4.2)
        ├── core/
        │   └── walletCoreClient.ts # typed postMessage wrapper the rest of the app calls - never touches WASM directly
        ├── api/                    # wallet-backend REST client: auth, users, payments, assets, swaps
        ├── connectivity/
        │   └── connectivityMonitor.ts # navigator.onLine + active health-ping against GET / (§6.4)
        ├── cache/
        │   └── offlineCache.ts     # IndexedDB store for last-known-good balances/tokens/history (§6.4) - non-secret, separate from the vault
        ├── store/                  # Redux Toolkit - UI/app state ONLY (see §6.2 for the hard boundary); includes connectivity status (§6.4)
        ├── components/             # Button, ButtonSecondary, TextInput, Brand, ConnectivityIndicator - ported theme (§1.1)
        └── pages/
            ├── onboarding/          # create new wallet vs. import existing
            ├── importSigner/        # signer mnemonic import + password set
            ├── importPrimaryWallet/ # primary wallet mnemonic import (§3 mode 2) or "same as signer" (mode 1)
            ├── unlock/               # password-gated unlock screen (shown on every fresh session) - works fully offline
            ├── dashboard/            # balances, curated tokens, payment history - live when online, cached-with-timestamp when offline (§6.4)
            ├── send/                 # build → sign (via worker) → submit payment flow - disabled while offline (§6.4)
            ├── swap/                 # build → sign → submit approval + swap flow - disabled while offline (§6.4)
            └── settings/             # lock timeout, export/rotate, remove wallet (wipe)
```

### 6.2 The Redux boundary (fixes §1.2 finding 3)

Redux (and all React component state) holds **only**: which pages/roles
are unlocked (a boolean, not the key), public addresses, fetched
balances/prices/history, form field values for non-secret inputs, and UI
state (loading, toasts, modals). A mnemonic or password is only ever
resident in a local component field for the seconds it takes to submit it
to `walletCoreClient` (which forwards it into the Worker and never
returns it), and is cleared from that field immediately after. Nothing
secret is ever the payload of a Redux action — enforced by convention now,
and worth a lightweight lint rule or code-review checklist item at
implementation time (e.g. grep for `mnemonic`/`secretKey`/`privateKey` in
any `store/*Slice.ts` action payload type).

### 6.3 Transaction signing flow (payments/swaps) — corrected for a Safe-based primary wallet

Since `wallet-backend/PLAN.md` §13 makes the primary wallet a Safe
smart-contract account (§3 above), a primary-wallet payment/swap is a
Safe transaction, not a directly-signed EIP-1559 send:

1. UI calls `wallet-backend`'s `POST /payments/build` (or `/swaps/
   approve/build`) → gets back a Safe transaction to sign: the
   `to`/`value`/`data`/`nonce` fields plus the EIP-712 domain/type data
   needed to compute its `SafeTxHash`, rather than a bare
   `network.UnsignedTx`.
2. UI passes that JSON to `walletCoreClient.signTypedData(role,
   typedDataJson)` (§4.2's new export) - always `role: "signer"`, since
   that's the only key that exists; there is no separate primary-wallet
   key to choose between anymore.
3. The Worker's `wallet-core` instance (already unlocked, or the UI
   prompts for the password first if not) signs the typed data and
   returns only the signature.
4. UI calls `POST /payments/submit` (or `/swaps/approve/submit`) with
   that signature. `wallet-backend` assembles and submits the Safe's
   `execTransaction` call (gas-sponsored per `wallet-backend/PLAN.md`
   §13.7); `wallet-payment-history-engine` picks up the resulting
   on-chain event independently.

A sub-wallet's payment/swap (once sub-wallets are supported in this
app's UI - tracked, not yet scheduled) follows the identical shape: the
signature is still produced by the `signer` role's key, resolved through
the sub-wallet's nested EIP-1271 ownership chain back to the primary
wallet (`wallet-backend/PLAN.md` §13.3) - nothing about *this app's*
signing flow changes between "pay from my primary wallet" and "pay from
my sub-wallet."

At no point does any signature, mnemonic, or private key touch
`wallet-backend` in a form it could reuse — only addresses, unsigned
transaction/typed-data requests, and signatures ever cross the network
boundary, in either direction.

### 6.4 Connectivity awareness: offline login/view, online-only transactions

Three distinct capabilities need three distinct network requirements, and
conflating them is exactly the kind of ambiguity that leads to either a
frustrating "can't do anything without internet" wallet or an unsafe
"builds a transaction against stale data while offline" one:

| Capability | Network required? | Why |
|---|---|---|
| Unlock (decrypt a role's vault) | No | Pure local Argon2id + AES-GCM operation inside the Worker (§5) — no call to `wallet-backend` at all. |
| View wallets (addresses, last-fetched balances/tokens/history) | No | Rendered from `cache/offlineCache.ts` (below) when offline; refreshed from `wallet-backend`/`wallet-payment-history-engine` when online. |
| Any transaction (payment build/sign/submit, swap approve/build/sign/submit) | **Yes, verified** | Building a tx needs a fresh server-resolved nonce and current EIP-1559 gas fees (§2's `/payments/build`); submitting needs to broadcast. Signing an unsigned tx offline against stale gas/nonce data would silently risk a failed or stuck transaction, so the whole flow — not just the network calls inside it — is gated on confirmed connectivity, per this plan's opening requirement (§0). |

**Detecting connectivity** (`connectivity/connectivityMonitor.ts`):
`navigator.onLine`/the browser's `online`/`offline` events are a fast
*passive* signal but only reflect the network interface's state, not
whether `wallet-backend` is actually reachable (a captive portal, a VPN
with no route to the backend, or the backend itself being down all leave
`navigator.onLine` reporting `true`). So this module layers an *active*
check on top: a lightweight `GET /` against `wallet-backend`'s existing
health endpoint (`internal/components/root/controllers.go`, already
returns `{service, status, chainId}` with no auth required), polled on an
interval, re-checked immediately whenever the passive signal flips to
`online` or the tab regains focus/visibility, and backed off
(exponentially, capped) while it keeps failing so a prolonged outage
doesn't mean constant background requests. The result is a single
tri-state `ConnectivityStatus` (`"online" | "offline" | "checking"`)
held in Redux — connectivity is not secret, so it lives alongside the
rest of the app's non-sensitive UI state (§6.2) without conflict.

**The indicator**: a small, always-visible status chip (topbar, next to
the account/brand area) — a colored dot plus a one-word label, in the
existing theme's idiom: `trovored.primary` (already the app's alert/
negative color) for "Offline", a new `positive`/`success` green token for
"Online" (the current theme has no positive-state color defined —
one plain addition to `tailwind.config.js`'s color scale, decided at
implementation time), and a neutral `primary-300` pulse for "Checking…".
Hovering/tapping it shows when the last successful check was.

**Enforcement, not just display**:
- The Send and Swap pages (and their entry points from the dashboard)
  read `ConnectivityStatus` and render their primary action disabled with
  an explanatory tooltip ("Connect to the internet to send a payment")
  whenever it is not `"online"` — checked again, authoritatively, by the
  API client immediately before the `/build` call itself, so a status
  flip during the brief window after render can't let a stale "online"
  render start a doomed request.
- `wallet-core`'s signing functions (§4.2) are not themselves
  network-aware — they'll happily sign whatever unsigned tx JSON they're
  given, offline or on. The online requirement is enforced at the app
  layer (the UI won't reach the build step, and the build step itself
  physically cannot succeed offline since it's a `wallet-backend` call),
  not inside `wallet-core` — keeping `wallet-core`'s API surface (§4.2)
  free of a connectivity concept it doesn't need to know about.

**Offline read cache** (`cache/offlineCache.ts`): a separate IndexedDB
store from the encrypted vault (§5.1) — this one holds plain, non-secret
data: the last-fetched curated token list, balances, and payment-history
page(s), each stamped with the timestamp of that fetch. Every successful
online read through `api/` writes through to this cache; the dashboard
reads from it directly whenever `ConnectivityStatus` is not `"online"`,
and always renders a visible "as of {timestamp}" note on cached data so a
user offline is never left thinking they're looking at a live balance.
Nothing in this cache is sensitive enough to need the vault's Argon2id/
AES-GCM treatment — addresses and public on-chain data are not secrets —
but it is still scoped per-signer (keyed by the signer's address) so
switching wallets doesn't briefly flash the previous wallet's cached
numbers.

## 7. Threat model and hardening checklist

| Threat | Mitigation |
|---|---|
| XSS reading key material from page memory/state | Worker isolation (§4.1); nothing secret in Redux/React state/`localStorage` (§6.2) |
| Malicious/compromised npm dependency | Minimize main-thread dependency surface for anything touching `walletCoreClient`; the actual crypto lives in one pinned, from-source-built WASM artifact, not N separate JS crypto packages |
| Stolen/leaked encrypted vault (e.g. exfiltrated IndexedDB) | Argon2id (§5.2) makes offline brute-force expensive; a random per-record salt (fixes §1.2 finding 2) defeats precomputation |
| Brute-forcing the unlock password in-session | Exponential backoff on failed unlock attempts (§5.3) |
| Session left unlocked on a shared/public machine | Idle-timeout auto-lock, cross-tab lock propagation, always-locked-on-load (§5.3) |
| Phishing (fake site asking for a mnemonic) | SIWE's own domain-binding (`wallet-backend`'s `Verify` already checks `msg.GetDomain()`) gives users a real signal to check; onboarding copy explicitly warns "this app will never ask for your mnemonic anywhere except the one-time import screen" |
| Imported mnemonic derives an unexpected address | Standard BIP-44 path only, no custom derivation (§4.4); `derive_preview_address` shown before committing to import |
| Malicious page script abusing a legitimate unlocked session | Narrow, fixed WASM API surface (§4.2) — no generic "sign arbitrary bytes" primitive exposed beyond the specific SIWE/tx/typed-data shapes the app itself constructs |
| Compromised CDN serving a tampered WASM binary | Self-hosted, same-origin `wallet-core.wasm` (matching the existing `web` app's own nginx/Docker self-hosting pattern, `web/Dockerfile`, `nginx.conf.template`) rather than a third-party CDN; reproducible build in CI |
| Inline-script injection | Strict CSP (`script-src 'self'`, no `unsafe-inline`, no `unsafe-eval` — WASM instantiation via `instantiateStreaming` does not require `unsafe-eval`) |
| Clipboard-based mnemonic exfiltration during import | Paste is allowed (blocking it is often more theater than defense and hurts recovery-phrase-restore UX), but the app clears the OS clipboard automatically a short time after any of its own copy-to-clipboard actions and never programmatically reads the clipboard itself |
| A user acting on stale offline data (e.g. believing a cached balance is current) | Offline-rendered data is always timestamped ("as of …", §6.4); no transaction can be built or signed while offline at all, so stale data can influence a *decision* to transact later but never the transaction's actual parameters (those are always re-resolved fresh at build time) |
| Spoofed "online" status tricking the app into attempting a transaction | The connectivity indicator (§6.4) is advisory for the UI, not authoritative for the network call — the API client independently verifies reachability immediately before `/build`, so a stale or manipulated client-side status flag can at most cause a build request that then fails cleanly, never a transaction signed against assumptions that were never actually verified |

## 8. Explicitly out of scope for `wallet-web` v1

- **Shared-access group wallets** (`wallet-backend`'s `sharedaccess`
  component) — a real, already-built feature, but its own multi-party
  approval UI is a distinct enough surface to be its own follow-up rather
  than bundled into the initial non-custodial signer/primary-wallet
  flow this plan covers.
- **Hardware wallet support** (Ledger/Trezor WebUSB) — the encrypted
  software-vault path is v1; a hardware-signer path would bypass
  `wallet-core`'s vault entirely (the device holds the key, not this
  app) and is a separate, additive design.
- **BIP-39 passphrase ("25th word")** — `wallet-core`'s API is shaped to
  accept one later (an optional parameter alongside `phrase`), but v1
  ships without it to keep the import UX simple.
- **Mobile app** — `trovo-wallet-monorepo/mobile` is a separate codebase
  and out of scope; nothing here assumes React Native compatibility.

## 9. Phased build roadmap

| Phase | Scope | Depends on |
|---|---|---|
| 1 | `wallet-core` crate: mnemonic generate/validate, BIP-44 derivation, address preview (§4.3, §4.4) | — |
| 2 | `wallet-core` vault: Argon2id + AES-256-GCM encrypt/decrypt/zeroize (§5) | Phase 1 |
| 3 | `wallet-core` signing: EIP-191 SIWE signatures, EIP-1559 tx signing matching `network.UnsignedTx`'s shape (§4.3, §6.3) | Phase 2 |
| 4 | Worker host + `walletCoreClient` typed RPC boundary (§4.1, §4.2, §6.2) | Phase 3 |
| 5 | App scaffold: Vite/React/TS/Redux Toolkit, ported theme (Tailwind config, fonts, `Button`/`TextInput`/brand components) (§1.1, §6.1) | — (parallel with 1-4) |
| 6 | Onboarding + import flows: create new wallet, import signer, import primary wallet (same-mnemonic and separate-mnemonic modes per §3), unlock screen, idle-lock wiring | Phases 4, 5 |
| 7 | `wallet-backend` API client: SIWE login, registration, `/users/wallets/link-primary` (calls the endpoint from §3 once it exists; degrades to same-mnemonic-only mode until then) | Phase 6 |
| 8 | Connectivity module: passive + active (health-ping) detection, `ConnectivityStatus` in Redux, the online/offline/checking indicator component (§6.4) | Phase 5 |
| 9 | Dashboard: balances/curated tokens (`/assets`), payment history (`/payments/history/:address`), offline read cache with "as of" timestamps (§6.4) | Phases 7, 8 |
| 10 | Send flow: build → sign (worker) → submit (§6.3), gated on `ConnectivityStatus` end-to-end (§6.4) | Phases 7, 8 |
| 11 | Swap flow: approve build/sign/submit → swap build/sign/submit, same connectivity gating | Phase 10 |
| 12 | Hardening pass: CSP, failed-unlock backoff, cross-tab lock sync, clipboard auto-clear, security-focused code review against §7's checklist | Everything above |
| 13 | Full integration pass: build/lint/test, a live Base Sepolia smoke test (import a real testnet mnemonic, send a real testnet payment end-to-end, then verify offline login/view and blocked transactions under simulated offline conditions), README, this file's own "Implementation notes" section | Phase 12 |

Each phase, when implementation is authorized, follows this project
family's established discipline: build clean → test → document in this
file's own "Implementation notes" → commit and push. Implementation does
not begin until explicitly authorized.

## 10. Implementation notes

Phases 1-13 are implemented as designed above, with the following notes:

- **BIP-32/44 HD derivation is hand-rolled** (`wallet-core/src/mnemonic.rs`)
  against the raw spec (HMAC-SHA512 + `k256` scalar/point arithmetic)
  rather than pulling in a third-party HD-wallet crate, per §4.3's
  dependency-minimization call. Verified against the standard BIP-39 test
  mnemonic ("abandon ... about"), which derives the exact well-known
  `m/44'/60'/0'/0/0` address every other tool derives for it - confirming
  §4.4's "same path everyone else uses" requirement is actually met, not
  just intended.
- **The EIP-1559 RLP encoder is also hand-rolled** (`rlp.rs`), for the
  same reason. Its test suite includes a full round-trip: sign a
  transaction, RLP-decode the result back out, recompute the unsigned
  payload hash independently, and confirm the embedded signature actually
  recovers to the signing key's address - not just that the output "looks
  like" a transaction. This test caught a real bug in itself during
  development (the test's own re-encoding of decoded RLP items, not a
  bug in the signing code), which is exactly the kind of mistake a
  weaker "does it look plausible" test would have missed.
- **`wallet-core`'s wasm-bindgen surface deviates from §4.2's illustrative
  API in one respect**: storage (writing/reading a `VaultRecord` to/from
  IndexedDB) is NOT done inside the WASM module - it stays in the
  Worker's own TypeScript (`worker/vaultStorage.ts`). WASM has no
  IndexedDB binding without pulling in extra web-sys/idb dependencies for
  a concern that has nothing to do with cryptography; keeping storage in
  the Worker's TS instead means `wallet-core` only ever produces/consumes
  plain JSON strings, and stays portable to a native Rust test build with
  zero browser API surface.
- **`encrypt_vault` unlocks immediately after creating a vault**, using
  the password already in hand, rather than requiring a second UNLOCK
  round trip with the same password moments later (e.g. right before the
  SIWE sign-in that follows signer creation in the onboarding wizard).
  Not a new capability - creating a vault already required the plaintext
  phrase - just avoids asking the user for a password they just typed a
  second time in the same flow.
- **Failed-unlock backoff (§5.3) lives in the Worker**, not in
  `wallet-core` itself: `failedAttempts`/`lastFailureAt` maps keyed by
  role, in the Worker's own memory, checked before every UNLOCK call and
  reset on success. Kept out of the WASM module since it's session
  bookkeeping, not cryptography.
- **A real bug was caught by live browser testing, not just unit tests**:
  the initial router guard redirected away from the onboarding wizard the
  moment the *signer* vault existed, before the wizard reached
  primary-wallet setup - because creating the signer vault immediately
  set `wallet.signer.hasVault = true` in Redux, and the guard was keyed
  on that alone. Fixed by keying "onboarding complete" on **both** roles
  having a vault, not just the signer - which also correctly resumes
  onboarding (rather than dead-ending at the Unlock screen) if a user
  reloads mid-flow having created a signer vault but not yet a primary
  one. Caught via a headless-browser smoke test (Playwright) that
  actually clicked through wallet creation end-to-end against a running
  `vite preview` server - a pure type-check would never have caught it,
  since every individual piece was correctly typed.
- **Verified live in a real browser** (Playwright + Chromium against
  `vite preview`, no mocking): mnemonic generation and the confirm-by-
  retyping step render and validate correctly; vault creation produces a
  real IndexedDB record (role, checksummed address, Argon2id params,
  random salt/nonce, ciphertext) confirmed by reading it back directly
  from the browser's IndexedDB; a SIWE sign-in attempt against an
  unreachable `wallet-backend` fails cleanly with a caught, displayed
  error and zero uncaught page errors - exercising the exact WASM →
  Worker → IndexedDB → network pipeline the whole security architecture
  depends on, not just its Rust-side unit tests in isolation.
- **Not verified live**: the actual round trip against a running
  `wallet-backend` instance (registration, a real Base Sepolia payment
  build/sign/submit, `/users/wallets/link-primary` once it exists) - this
  sandbox has no `wallet-backend` instance running to test against.
  Recommend running the full onboarding → send flow against a real
  deployment before shipping.
- **Two known, non-blocking dependency advisories** (`npm audit`):
  `react-router-dom`'s and Vite's transitive `esbuild`'s currently-open
  CVEs both require a major-version bump to fully resolve and have
  minimal exploitability in this app's actual usage (no SSR, no
  user-controlled navigation targets, dev-server-only `esbuild` issue) -
  tracked in `README.md` rather than silently ignored.
- **Deliberate scope reduction**: the onboarding wizard's step state is
  component-local, not persisted - a user who abandons the flow after
  creating a signer wallet but before finishing primary-wallet setup
  resumes at step 1 on reload rather than exactly where they left off
  (their signer vault is preserved, not lost). A UX polish item, not a
  security gap, left for a later pass given this plan's already-large
  scope.
- **In-app testnet/mainnet switch added** (`src/config/network.ts`,
  a Settings-page toggle in `src/pages/settings/Settings.tsx`): both
  networks' `wallet-backend` URL and chain ID ship in one build
  (`VITE_WALLET_BACKEND_URL_TESTNET`/`_MAINNET`,
  `VITE_CHAIN_ID_TESTNET`/`_MAINNET`), and the active choice is a
  `localStorage`-persisted runtime toggle, not a rebuild - `httpClient.ts`
  reads the active network's URL fresh on every request instead of
  caching it at module load. Switching networks reloads the page rather
  than trying to reconcile in-memory Redux/cache state, since testnet and
  mainnet are separate `wallet-backend` deployments with entirely
  separate user registrations, not just a different chain ID on one
  shared server - there's nothing to "translate" from one to the other.
  The toggle disables whichever network's backend URL is left empty, so
  an unconfigured build can never silently offer a network nobody set up
  for it. `app/nginx.conf.template`'s CSP `connect-src` and
  `Dockerfile`'s app-builder stage were updated to carry both networks'
  origins/build args side by side (`DEPLOYMENT.md` §2-4, §9). This is
  orthogonal to and does not resolve §11 below - the switch changes which
  backend/chain the app targets, not what auth scheme it speaks to that
  backend.

## 11. `wallet-backend`'s auth redesign - implemented

`wallet-backend/PLAN.md` §12 replaced SIWE+session-JWT with a per-request
signature model (personal_sign/secp256k1, a signer/wallet header split).
This app's client code has been updated to match:

- Removed `api/siwe.ts`, `api/authFlow.ts` (deleted entirely - both were
  SIWE-specific), `api/authApi.ts` (deleted - its only exports were the
  nonce/verify calls), `authSlice.ts`'s `sessionToken` (the slice now
  only tracks `username`, set by a new `profileRegistered` action), and
  the onboarding wizard's separate "sign in" step - it moves straight
  from `createVault('signer', ...)` to checking whether a profile
  already exists (`getUser`) or registering one, no login round trip.
- `api/httpClient.ts` now signs every authenticated request itself:
  `apiRequest`'s `token` option became `walletAddress` (the wallet the
  request acts on, wallet-backend's `X-Wallet-Address`); when present it
  builds `path + signerAddress + timestamp` (`path` already includes any
  query string, matching `internal/middleware/signature_auth.go`'s
  `c.Request.URL.RequestURI() + signer + timestampStr` exactly), signs it
  with the **signer** role's key via the Worker, and attaches
  `X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp`. Every
  call site in `usersApi.ts`/`assetsApi.ts`/`paymentsApi.ts`/
  `swapsApi.ts` and their callers (`OnboardingWizard.tsx`, `Send.tsx`,
  `Swap.tsx`) updated accordingly - `Send`/`Swap` now gate on the signer
  vault being unlocked and a primary wallet address being known, rather
  than a session token.
- `wallet-core`'s `sign_siwe_message` renamed to `sign_request_message`
  (`lib.rs`) now that SIWE-specific signing is gone entirely - it was
  always message-agnostic EIP-191 `personal_sign`, so this is a rename
  only, no behavior change; `wasm-pkg` rebuilt via `wasm-pack build
  --release --target web`. `vault.rs`/`mnemonic.rs`/`signing.rs`
  themselves needed no change - this was a client-orchestration change,
  not a cryptographic one. All 21 `wallet-core` Rust tests still pass.
- The property called out below as traded away (SIWE's `domain` binding)
  is accepted as-is - still true, still only matters if a
  browser-extension wallet is ever added as a second client.
- **Verified with a real, live round trip**, not just a build/typecheck:
  booted an actual `wallet-backend` instance (SQLite) locally, built this
  app against it, and drove the real onboarding UI end-to-end with
  Playwright + Chromium (real WASM signing in the real Worker, not
  mocked) - `GET /v1/users/:address` correctly 404s for a fresh signer
  address, and `POST /v1/users` returns `201 Created` with the
  SignatureAuth headers this app now builds, confirming the message
  format matches wallet-backend's middleware exactly and the flow
  reaches the primary-wallet step with no login step in between.
- **Follow-up fix caught by extending that same live test through to the
  Dashboard**: `api/paymentsApi.ts`'s `getPaymentHistory` was the one call
  site missed in the initial pass - it called `GET
  /v1/payments/history/:address` with no `walletAddress`, so it went out
  unsigned. This route is under `internal/components/payments/
  controllers`'s `authed` group (`middleware.SignatureAuth`), unlike `GET
  /v1/assets` and `GET /v1/assets/balance/:address`, which really are
  public - so this one silently 401'd for as long as Dashboard's own
  try/catch-and-fall-back-to-cache masked it. Fixed by passing
  `walletAddress: address` through; re-verified live by driving the full
  onboarding flow through to a loaded Dashboard - the same request that
  used to fail now returns `200 []` against a fresh account. Audited
  every other `apiRequest` call site in the app against wallet-backend's
  actual route groupings at the same time; nothing else was missing it.

One property is traded away, not lost by oversight: SIWE's `domain`
field lets a signing wallet show "you are signing in to
wallet.example.com" - a real phishing signal. A generic
path+address+timestamp message has no such binding. This doesn't matter
for this app's own embedded signer (§4.1 - it already signs silently in
a Worker with no human-reviewed prompt either way), but would matter if
a browser-extension wallet (MetaMask et al.) were ever added as a second
client against the same API, since that class of client *does* show the
user what they're signing.

## 12. `wallet-core` gains an FFI target and an EIP-712 signer (tracked, shared with `wallet-mobile`)

`wallet-backend/PLAN.md` §13 (Base equivalent of the original's
sub-wallet/shared-access multisig, using Safe smart accounts) and the new
`wallet-mobile/PLAN.md` (a Flutter port of the original's mobile app,
reusing this crate rather than re-implementing its own crypto) both
create requirements on `wallet-core` that belong here, in the crate's own
project, not duplicated into either consumer's plan:

- **New: EIP-712 typed-data signing - broader than originally scoped
  here.** Originally flagged as needed only for approving a Safe-based
  shared-access pending action (`SafeTxHash` signing, EIP-712, not plain
  EIP-191 `personal_sign` - `signing.rs` only has
  `sign_personal_message`/`sign_eip1559_transaction` today). Once
  `wallet-backend/PLAN.md` §13 makes the *primary* wallet a Safe too (not
  only sub-wallets - a correction driven by §15's wallet-recovery
  design), this app's own §6.3 needs it for **every** primary-wallet
  payment and swap, not only shared-access approvals - this app's own
  `sign_transaction` (raw EIP-1559) call for payments/swaps is being
  replaced by this function, not supplemented by it. This is now a
  same-priority dependency as the FFI target below, not a
  `wallet-mobile`-driven nice-to-have.
- **New: an FFI binding target** (`wallet-mobile/PLAN.md` §4.1 recommends
  `flutter_rust_bridge`) alongside the existing `wasm-pack` build - this
  is an additional build target for the same source, not a fork of it.
  No function this app calls today changes shape because of this.
- **Possible rename**: `sign_siwe_message` is already message-agnostic
  EIP-191 `personal_sign` (§11 above) and is the function both
  `wallet-backend`'s new per-request signature scheme (§11) and
  `wallet-mobile`'s equivalent networking layer sign against. If it gets
  renamed for clarity (e.g. `sign_request_message`), do it once here so
  neither consumer drifts from the other's expectation of the function's
  name.

Not implemented here - §4.2/§6.3 above already describe the corrected,
target API and flow; this section exists so the crate-level
implementation work itself isn't planned twice in two different
consumers' documents once `wallet-backend`'s Safe-based redesign (§13
there) actually starts.

## 13. Wallet recovery UI (design - implemented in §16)

`wallet-backend/PLAN.md` §15 documents **two coexisting recovery
branches**, not one - both worth UI here, presented as genuinely
different options with different tradeoffs, not one being an upgraded
version of the other in the UI copy:

- **Branch A - free, DB-only address swap** (already built server-side,
  `recovery.go`): trades continuity for simplicity - the account gets a
  fresh address, and any funds/sub-wallets/shared-access memberships at
  the old one are abandoned, not carried over. No settings/enrollment
  screen beyond the existing enable/disable toggle - the security
  questions setup it depends on may already exist from account
  registration.
- **Branch B - paid, true wallet recovery** (new, §15.5): enroll a
  recovery-service Safe as a second owner of the primary wallet, so a
  lost signer key can later be replaced (`swapOwner`) without losing the
  wallet's address or its sub-wallets/shared-access memberships (§13.3's
  nested-ownership design means recovery only ever touches the primary
  wallet's own Safe - nothing else needs re-pointing). Needs its own
  enable/disable settings screen (the one-off fee disclosure, and a
  plain-language explanation of what the recovery service can and
  cannot do per §15.3 - especially if the recommended Safe Guard
  restricting it to owner-management calls only is implemented, since
  that's a genuine, worth-advertising guarantee).

Both branches share one recovery execution flow reachable *without*
being logged in (by definition, the user has no working signer key at
this point): security questions, email OTP, and a **new** signer key
generated fresh in `wallet-core` right there in that flow, ending with a
`personal_sign` proof from that new key (`wallet-backend/PLAN.md` §15.5
step 1) - this is the one place in this app where a brand-new vault gets
created *before* any successful login, not after one. The UI's job is
presenting the choice plainly (same address, sub-wallets, and shared
access preserved vs. a fresh start) before the user picks which branch
to invoke, since the two produce materially different outcomes for the
same starting problem.

Not implemented here - tracked as a dependency this app's own
implementation will need once `wallet-backend`'s §15 lands, the same way
§11/§12 track that project's other pending dependencies.

## 14. Closing the two-mnemonic model and wiring the Safe-based payments/swaps fix

`wallet-backend/PLAN.md` §17 closed a severe bug there: `payments`/
`swaps`/`assets/approve` built plain EIP-1559 transactions "from" a Safe
wallet address, which is unsignable (a Safe has no private key). Fixing
that changed `/build`'s response shape (`{actionId, digestToSign}`
instead of a raw unsigned transaction) and `/submit`'s request shape
(`{actionId, signature}` instead of `{signedTx}`). This app's own
`OnboardingWizard.tsx` predated §13's Safe redesign entirely and still
implemented §3's since-corrected two-mnemonic model (import/create a
*separate* "primary wallet" phrase, call a `link-primary` endpoint that
never existed server-side) - both sides needed a full pass, not just the
response-shape update.

- **Removed the vestigial second-mnemonic onboarding steps entirely**
  (`'primary-choice' | 'primary-password' | 'import-primary' |
  'primary-password-separate'` from `OnboardingWizard.tsx`'s `Step`
  union, and `usersApi.ts`'s dead `linkPrimaryWallet`/
  `LinkPrimaryNotSupportedError`) - there is no local key for the
  primary wallet to hold, so there was never anything for a second
  mnemonic to do. `finishWithPrimaryWallet` now records the
  wallet-backend-*computed* Safe address (`user.address` from
  `registerUser`/`getMyUser`) as `wallet.primary`, reusing the
  `vaultCreated` Redux action as bookkeeping convenience only -
  `hasVault`/`isUnlocked` being `true` for `primary` means "we know its
  address," not "a vault exists," since a Safe has nothing to unlock
  (`walletSlice.ts`'s `RoleState` doc comment spells this out so it isn't
  rediscovered as a bug later). Dropped `walletSlice.ts`'s now-meaningless
  `primarySameAsSigner` field along with it.
- **`paymentsApi.ts`/`swapsApi.ts`/`assetsApi.ts` updated to the new
  `{actionId, digestToSign}` / `{actionId, signature}` shapes** (a shared
  `ActionProposal` type in `paymentsApi.ts`, imported by the other two).
  `Send.tsx`/`Swap.tsx` now `signRequestMessage('signer', digestToSign)`
  instead of `signTransaction('primary', ...)` - approval is a plain
  `personal_sign` over a digest (`sharedaccess.DigestToSign`'s own design,
  see `wallet-backend/PLAN.md` §17), not EIP-712 typed-data signing, so no
  new `wallet-core` primitive was needed despite this and the sibling
  projects' own earlier planning assuming otherwise.
- **A real bug in the existing-user lookup, found while wiring this**:
  `handleSignerPasswordSubmit` called `getUser(address)` - `address`
  being the *signer's* address - against `GET /v1/users/:username`,
  which only ever resolves by the literal `username` column. Every
  re-import of an already-registered signer therefore always 404'd and
  fell through to the registration branch instead of recognizing the
  existing account (which would then itself fail server-side on the
  already-registered address). Fixed by adding `GET /v1/users/me`
  server-side (self-signed, resolves by `CtxSubject`; see
  `wallet-backend/PLAN.md` §17) and a matching `getMyUser()` here -
  `getUser(username)` itself is now dead code (nothing else called it)
  and was deleted rather than left as an unused export.
- **A second real gap, found only by driving the flow live**: a fresh
  registration leaves the Safe undeployed (`PrimaryWalletDeployed =
  false` - `wallet-backend/PLAN.md` §13.11's deliberate
  "registration never requires activation"), and an undeployed Safe has
  no `sharedaccess` `ClosedGroup` row yet either, so `/build` 404s with
  "no shared-access group found for this wallet address" for anyone who
  hasn't separately called the self-service
  `POST /v1/users/wallet/deploy`- which this app never called at all.
  Added `deployPrimaryWallet()` to `usersApi.ts` and two call sites: a
  best-effort, non-blocking call at the end of onboarding (deployment is
  idempotent and platform-fee-paid, so firing it early costs nothing and
  usually means it's already done by the time a user first tries to pay),
  and `usersApi.ts`'s `withWalletDeployRetry()` wrapping `buildPayment`/
  `buildSwap` in `Send.tsx`/`Swap.tsx` as the real backstop - it catches
  that specific 404, calls `deployPrimaryWallet`, and retries the build
  once. Not wired into `assetsApi.ts`'s `buildApprove` since nothing in
  this app calls it yet (dead code, pre-existing, out of scope here).
- **`App.tsx`'s reload hydration no longer checks a vault that will never
  exist**: it previously called `hasVault('primary')`/
  `getKnownAddress('primary')` (the IndexedDB-backed vault system) to
  decide whether onboarding was complete, which is always `false` now
  that no local vault is ever created for `primary` - every full page
  reload incorrectly bounced straight back to `/onboarding`, discovered
  by extending the live E2E test past the initial onboarding-to-Dashboard
  path. Fixed by resolving the primary address from the offline,
  non-secret IndexedDB cache (`cache/offlineCache.ts`'s new
  `cacheKeys.primaryWalletAddress(signerAddress)`, written by
  `finishWithPrimaryWallet` at the end of onboarding) first, falling back
  to a live `GET /v1/users/me` (caching its result) only if nothing is
  cached yet; primary's `isUnlocked` is now derived from "is its address
  known" rather than tracked separately, matching signer's real
  vault-backed unlock but without inventing an unlock step Unlock.tsx
  would otherwise present for a wallet with nothing to unlock (`Unlock.tsx`
  needed no code change - its `hasVault && !isUnlocked` filter already
  never selects `primary` once the two flags move together).
- **Verified live**, not just build/typecheck: booted a real
  `wallet-backend` instance (SQLite) and drove the actual UI with
  Playwright + Chromium through registration → Dashboard → a full page
  reload → Send, confirming (a) onboarding reaches Dashboard directly
  with none of the removed primary-choice steps, (b) a reload now
  correctly lands on `/unlock` rather than bouncing to `/onboarding`, and
  (c) `Send`'s `buildPayment` call, `withWalletDeployRetry`, and
  `deployPrimaryWallet` all fire in the right order end-to-end - the
  Safe deployment itself fails cleanly with wallet-backend's own honest
  error (`fetch nonce: ... Forbidden`) because this sandbox's egress
  policy blocks `sepolia.base.org`, the same pre-existing, documented
  limitation noted throughout this document and `wallet-backend/PLAN.md`
  §17 - not a bug in this fix. `npx tsc -b` and `npm run build:app-only`
  both clean throughout.

## 15. CRITICAL FIX: digest-signing used the wrong EIP-191 byte convention

While starting on §13's wallet-recovery UI (which also needs to
`personal_sign` a `SafeTxHash` digest, exactly like §14's payments/swaps
fix), a close read of both sides of the signature check together turned
up a serious, previously-shipped bug: **every digest-based approval
signature this app has ever produced was rejected by `wallet-backend`.**
Payments, swaps, asset-approvals, and (about to be) wallet-recovery
enable/disable all silently failed at `sharedaccess.ApproveAction`'s
signature check - never caught, because backend unit tests sign with
Go-level test helpers matching the backend's own convention, and every
live E2E test run so far was blocked earlier by this sandbox's RPC
egress policy before ever reaching an actual `ApproveAction` call.

**The bug, precisely**: `sharedaccess.DigestToSign` (and wallet-recovery's
`*SafeTxHash` fields) return a `0x`-prefixed hex string representing a
32-byte hash. `Send.tsx`/`Swap.tsx` signed that hex *string* via
`signRequestMessage`/`wallet-core`'s `sign_personal_message`, which
applies EIP-191's `personal_sign` prefix using the string's own
**66-character ASCII length** and hashes its **ASCII bytes**. But
`wallet-backend`'s actual verification -
`cryptoutil.VerifyPersonalSignBytes(digest.Bytes(), ...)`, called by both
`sharedaccess.ApproveAction` and `wallet_recovery.go`'s
`verifySingleOwnerSignature` - applies the same EIP-191 prefix using the
**32-byte raw length** and hashes the **raw decoded bytes**. Same digest,
two different hashes, two different valid-looking signatures - the
signature `wallet-core` produced was entirely correctly formed, it just
never matched the hash the backend actually checked. `sign_request_message`
itself is *not* wrong - the SignatureAuth header message it's used for
elsewhere really is a human-composed string, correctly signed as ASCII
text, and `internal/middleware/signature_auth.go` verifies it the same
way. The bug was specific to reusing that same function for a
hex-encoded binary digest.

**Confirmed empirically, not just by code reading**: signed a fixed test
digest with the actual compiled `wallet-core` wasm running in a real
Chromium browser (Playwright, driving the real Worker - not a mock), then
fed that real signature straight into `wallet-backend`'s actual
`cryptoutil.VerifyPersonalSignBytes`/`VerifyPersonalSign` functions in a
Go test. Result: the old `sign_request_message`-based signature verifies
`true` against the (wrong) ASCII-string hash and `false` against the
(correct) raw-bytes hash `ApproveAction` actually uses - reproducing the
exact silent failure this fix closes.

**The fix**: added a second `wallet-core` signing primitive,
`sign_hex_digest` (`signing.rs`) - hex-decodes the digest first, then
applies the identical EIP-191 `personal_sign` construction to the raw
bytes, byte-for-byte matching `VerifyPersonalSignBytes`. Exposed via
`wasm_bindgen` alongside `sign_request_message` (both remain - one for
real message strings, one for hex-encoded digests, never interchangeable
again). New Rust test
`sign_hex_digest_matches_backends_raw_byte_convention` locks in the raw-
bytes convention and explicitly asserts it differs from the
string-based hash, so this can't silently regress back to signing the
wrong bytes. Threaded through `worker/protocol.ts` (`SIGN_HEX_DIGEST`),
`walletCoreWorker.ts`, and a new `signHexDigest()` in
`core/walletCoreClient.ts`; `Send.tsx`/`Swap.tsx` now call
`signHexDigest('signer', proposal.digestToSign)` instead of
`signRequestMessage`. Re-verified live the same way after the fix: the
real wasm's `sign_hex_digest` output now verifies `true` against
`VerifyPersonalSignBytes`.

**Also corrected**: `wallet-backend/PLAN.md` §17 previously claimed "no
new `wallet-core` signing primitive was needed at all" for the
sharedaccess-digest approach - that claim was the root cause of this bug
shipping in the first place (it justified reusing
`sign_request_message`/`signRequestMessage` for digests too) and has
been corrected there to point back to this section. Full
`cargo test` (wallet-core, 22/22), `go build`/`go vet`/`go test ./...`
(wallet-backend), and `npx tsc -b`/`npm run build:app-only` (this app)
all pass after the fix.

## 16. Wallet recovery UI - implemented

§13's design is now built:

- **`api/recoveryApi.ts`** (new): the full client surface for both
  branches - `listSecurityQuestions`/`setSecurityAnswer`/
  `verifySecurityAnswer`, Branch A's `enableAccountRecovery`/
  `disableAccountRecovery`/`requestAccountRecoveryOTP`/`recoverAccount`,
  and Branch B's `buildEnableWalletRecovery`/`confirmEnableWalletRecovery`/
  `buildDisableWalletRecovery`/`confirmDisableWalletRecovery`/
  `recoverWallet`. `usersApi.ts`'s `User` interface grew the
  `accountRecoveryEnabled`/`walletRecoveryEnabled`/`signerAddress`/
  `primaryWalletDeployed` fields wallet-backend already returned but this
  app never read; added `getUserByUsername` (the public
  `GET /v1/users/:username` lookup) for the recovery flow's own
  branch-availability check before it has any authenticated key to ask
  with.
- **`pages/settings/RecoverySettings.tsx`** (new, embedded in
  `Settings.tsx`): lets the signed-in owner answer security questions and
  toggle each branch. Branch B's enable/disable both `signHexDigest` the
  two `SafeTxHash` values a challenge returns - **not**
  `signRequestMessage`, the distinction §15 exists to enforce. A
  configured enrollment fee (`feeTx` present in the challenge) is
  detected and surfaced as an explicit, honest error rather than silently
  failing or half-implementing raw-transaction broadcasting - this app
  has no direct RPC client of its own (every other on-chain interaction
  goes through wallet-backend's relayer/deployer keys), and building one
  just for this one, likely-zero-configured-by-default case was judged
  out of scope here; tracked as a known gap, not silently dropped.
- **`pages/recovery/RecoveryWizard.tsx`** (new) + a `/recovery` route in
  `App.tsx` reachable regardless of onboarding/unlock state, linked from
  `Unlock.tsx`'s new "Lost your password or recovery phrase?" - by
  definition a user reaching for this has no working signer key to
  satisfy either of those gates. Flow: username → (if both branches are
  enabled) choose one → generate and confirm a **new** signer mnemonic
  (reusing `MnemonicReveal` as-is) → set its password → request an email
  OTP and answer security questions → submit. The new address's proof-of-
  ownership signature (`RecoveryMessage`) is `signRequestMessage`, a
  genuine human-composed string - unrelated to §15's digest-signing fix,
  confirmed by tracing `verifyNewAddressOwnership` server-side. On
  success, dispatches `vaultCreated` for both roles directly (no reload
  needed) and writes the offline cache entry `OnboardingWizard.tsx` also
  writes, so a subsequent reload resolves correctly through `App.tsx`'s
  existing hydration path.
- **A second real bug found live, not by inspection**: `GET /v1/users/me`
  (added in §14) looked correct but 404'd on its own primary use case.
  `getMe` read `GetByAddress(CtxSubject)`; for the only way to call this
  endpoint before a wallet address is known (a self-signed request,
  `X-Wallet-Address == X-Signer-Address`), `CtxSubject` resolves to that
  same signer value - which never matches `User.Address` (a distinct Safe
  address) once a profile actually exists. Every already-registered
  caller of `GET /v1/users/me` - `RecoverySettings.tsx`'s own refresh
  call included - got a false 404. Fixed server-side: added
  `GetBySignerAddress` and changed `getMe` to read `CtxSigner` (the
  actual verified EOA) instead of `CtxSubject`. New
  `TestGetBySignerAddress` regression test in `wallet-backend`.
- **Verified live end-to-end**, not just build/typecheck: booted a real
  `wallet-backend` (SQLite), drove the actual UI with Playwright through
  registration → Settings (save a security answer, enable Branch A,
  confirmed the fixed `GetBySignerAddress` correctly reflects "enabled"
  back in the UI) → `/recovery` (username → generate/confirm a new
  mnemonic → set its password → request a real OTP, read the actual code
  out of `wallet-backend`'s console-mailer log, answer the saved security
  question → submit) → **"Recovery complete"** with a real new wallet
  address, a full genuine round trip through `recoverAccount`'s three-
  factor verification. Branch B's enable/disable couldn't be driven this
  far live in this sandbox - both `buildEnableWalletRecovery` and
  `confirmEnableWalletRecovery` call `Blockchain.SafeNonce` before ever
  reaching signature verification, and that RPC call is blocked here -
  but the digest-signing convention it depends on was already proven
  correct independently in §15. `go build`/`vet`/`test ./...` and
  `npx tsc -b`/`npm run build:app-only` all clean throughout.

## 17. Theme/artifact audit against the real `trovo-wallet-monorepo/web`

`trovo-wallet-monorepo` is a separate, read-only reference repository -
never modified by this project, only read from - and its `web/` is the
actual original production app this rewrite is porting from. Prompted by
the equivalent audit done for `wallet-mobile` (which found real drift
against its own original), this pass compared `wallet-web`'s theme and
artifacts against that source directly rather than assuming the earlier
port was faithful:

- **Colors and fonts**: `tailwind.config.js`'s `primary`/`trovored` scale
  and `fontFamily` names are byte-for-byte identical to the real
  `web/tailwind.config.js` - unlike `wallet-mobile`, where the equivalent
  comparison caught a drifted `primary800`, this port's values were
  already correct. No change needed.
- **Logo**: `public/images/trovoLogo.png`, used by `components/Brand.tsx`,
  is confirmed byte-identical (`md5sum`) to the real app's own
  `public/images/trovoLogo.png`. Already correct.
- **Missing artifact found and added**: the real app's left-panel brand
  layout (`src/pages/importWallet/importWallet.tsx`) includes a
  `rafiki.png` illustration (a wallet/money graphic in matching
  trovoblue tones) below the title, which this port's `AuthLayout.tsx`
  had never carried over - its left panel was otherwise a faithful
  match (bg-primary-100, `Brand`, title, subtitle) but ended there. Added
  `rafiki.png` (copied byte-for-byte from the real app, confirmed by
  `file`/PNG-chunk inspection to carry no problematic embedded profile)
  to `public/images/` and rendered it in `AuthLayout.tsx`'s left panel,
  which is shared by onboarding, unlock, and recovery - all three now
  show it. The `hidden md:flex` collapse (this panel doesn't render
  below the `md` breakpoint) is unaffected.
- **Verified visually, not just by build**: `npm run build` (which
  chains `wasm-pack build` for `wallet-core` and `tsc -b && vite build`)
  is clean, and the built app was served with `vite preview` and driven
  with the pre-installed headless Chromium (`playwright-core`, installed
  with `--no-save` for this one-off check and removed again afterward -
  `package.json`/`package-lock.json` are unchanged) to screenshot the
  onboarding screen at both desktop (1280x800, illustration visible) and
  mobile (390x844, panel correctly hidden) viewports.

## 19. Closing the real feature gaps against the original app: tokenization, fiat/crypto funding, receipts

§17's audit only ever compared `wallet-web` against its own roadmap
(§9); it never diffed page-for-page against the real original app's own
router (`trovo-wallet-monorepo/web/src/routingSetup/appRouter.tsx`,
read-only, never modified). Doing that properly surfaced real gaps
beyond §8's three deliberate v1 exclusions (shared-access UI, hardware
wallets, BIP-39 passphrase):

- **Asset tokenization** - the entire `tokenize/apply` issuer flow and
  the investor `tokenizedAssets`/subscribe/early-exit flow - had zero
  `wallet-web` client despite `wallet-backend`'s tokenization component
  being fully built (§9 in that repo's own PLAN.md).
- **Fiat/crypto/Naira funding** - the original's Deposit/Withdraw modal
  in `components/walletOperations.tsx` - likewise had zero client
  despite `wallet-backend`'s fiat/stablerail/crypto components existing.
- **`pdfPages/sendAssetReceipt.tsx`** - a printable payment receipt -
  had no equivalent at all.
- **`add-remove-assets`** (Stellar trustline management) - investigated
  and found **not applicable on Base/EVM**: `assets.CuratedToken`'s own
  doc comment already states there is no EVM equivalent of a Stellar
  trustline gating which assets an address may hold (anyone can hold any
  ERC-20 balance with no opt-in step) - this is a real design difference
  between the chains, not a missed port, so no client was built for it.
- **`restore-inactive-account`** - a third account-recovery path -
  remains unbuilt on **both** sides: `wallet-backend` PLAN.md §15.9
  itself marks this phase "optional... worth confirming it isn't already
  redundant with Branch A before building a third mechanism" and never
  built it, so there is no endpoint for `wallet-web` to call yet. Left
  as a documented gap pending that backend decision, not silently
  dropped.

### 19.1 A real bug found while building this: tokenization/crypto's Safe-signing gap

Building the tokenization/funding UI against the real backend endpoints
surfaced that `tokenization.ConfirmApplication`/`BuildCryptoPurchase`/
`BuildEarlyExit` and `crypto.RequestWithdrawal` still had the exact bug
`wallet-backend` PLAN.md §17 already found and fixed in payments/swaps/
assets: building a plain unsigned transaction "from" the primary
wallet's Safe address and trusting a client-submitted signed transaction
as if a Safe could ever produce one. Fixed backend-side (delegating to
`sharedaccess`, same as payments/swaps) - see `wallet-backend/PLAN.md`
§18 for the full account. This app's new API clients were written
against the corrected propose/personal_sign-digest/confirm shape from
the start, not the broken one.

### 19.2 What was built

- **`api/tokenizationApi.ts`/`fiatApi.ts`/`stablerailApi.ts`/`cryptoApi.ts`**
  (new) - full clients for every endpoint these components expose.
  `tokenizationApi.ts`'s `TokenizedAsset` interface deliberately types
  only the fields this app's UI reads/writes, not the real row's
  hundreds of asset-class-specific columns (bond/fund/commodity/etc. -
  see `wallet-backend`'s `asset_class_fields.go`) - building a
  field-for-field replica of that form is a distinct, much larger
  follow-up, not attempted here; core deal-terms fields (name, code,
  description, sector/type, country, quote currency, quantities/price)
  are fully wired.
- **`pages/tokenize/TokenizeHome.tsx`** (new) - three tabs: my
  applications (list + delete a draft + link to detail), a new-
  application form (the core fields above), and my investments
  (purchases/expressions of interest/early exits, read-only lists).
- **`pages/tokenize/AssetDetail.tsx`** (new) - one asset by
  `:assetId`: issuer-side actions (confirm the application, sign+pay
  the application fee if one is owed, confirm fee payment once vetted)
  and investor-side actions (subscribe with crypto, express interest,
  early exit) gated on the asset's current status, all shown on the
  same page since ownership is enforced server-side either way. Every
  on-chain action (fee payment, purchase, early exit) follows Send.tsx's
  own build → `signHexDigest` → confirm pattern.
- **`pages/fund/FundWallet.tsx`** (new) - three tabs: Flutterwave fiat
  top-up (create an invoice, complete payment via the emailed link),
  Stablerail Naira (BVN onboarding + onramp, listing supported banks),
  and crypto (show a deposit address/history, and a real signed
  withdrawal debit via the same build/sign/confirm pattern).
- **`pages/receipt/Receipt.tsx`** (new) - shows a completed payment's
  from/to/amount/tx-hash-with-explorer-link/date, reached via router
  state from `Send.tsx`'s success screen or a `Dashboard.tsx` history
  row (not a URL param the way the original's `?q=<base64>` worked,
  since the record already lives in this app's own state at both call
  sites). Uses the browser's own print-to-PDF instead of adding
  `@react-pdf/renderer` as a dependency for one static document - a
  deliberate simplification (same end result, a PDF file, without a new
  dependency for a single page), not a missing feature.
- Wired into `App.tsx`'s router and `AppLayout.tsx`'s nav (`/fund`,
  `/tokenize`, `/tokenize/:assetId`, `/receipt`).

### 19.3 Verified

`npm run build` (wasm + tsc + vite) clean throughout every change above,
including after the backend rework. Live end-to-end: booted a real
`wallet-backend` (SQLite) locally, drove the actual UI with Playwright
through onboarding (create wallet → password → register) to a live
dashboard, then navigated to `/fund` and `/tokenize` and opened the new-
application form, screenshotting each to confirm real rendering (not
just a clean build) against the live backend - the same verification
discipline as §16's live recovery-flow check.

## 20. Closing §8's last real v1 exclusion: shared-access group wallets

Asked to resume outstanding implementation and audit `wallet-web` for
any remaining gap against the original app - not just the tokenization/
funding/receipt list from §19, but everything. That audit (documented in
full in `wallet-backend/PLAN.md` §19, since most of it turned on
checking what the original backend actually had a working *frontend*
for) resolved every remaining sidebar item except one:

- **Shared-access group wallets** - the one item §8 itself had already
  flagged as a real, deliberate v1 exclusion ("a real, already-built
  feature... a distinct enough surface to be its own follow-up"). The
  original's `dashboard/sharedAccess/{landing,add,update,walletInfo,
  approvals,approvalDetails}` pages are a complete, working feature, and
  `wallet-backend`'s `sharedaccess` component (PLAN.md §13's whole
  design) already implements every bit of backend support it needs.
  This is the one gap this pass actually closes.
- Patron ("Trovo Patron"), Market ("market-trade"), and "closed groups"
  sidebar links, the Yield/dividend page, and a Sumsub/Doja KYC page all
  turned out **not** to be real gaps once checked against the original's
  actual router (`appRouter.tsx`), not just its sidebar - see
  `wallet-backend/PLAN.md` §19 for the full accounting of each. Security
  questions are already covered by this app's own recovery redesign
  (§13/`RecoveryWizard.tsx`/`RecoverySettings.tsx`).

**A small real backend gap found while building this**: no
`wallet-backend` route ever exposed a group's membership list - only a
caller's own role, used internally for access checks. A member picking
"add member" or "change threshold" needs to see who's already on the
group first. Fixed on the backend side with
`Service.ListMembers`/`GET /v1/shared-access/groups/:groupId/members`
(`wallet-backend/PLAN.md` §19).

**What was built** - `src/api/sharedAccessApi.ts` (the full
`/v1/shared-access/*` client: `listWallets`, `createGroup`, `getGroup`,
`listMembers`, `getBalance`, `getCuratedBalances`, `proposePayment`/
`proposeContractCall`, `listPending`, `getAction`, `approveAction`/
`rejectAction`, and the four member-management proposal calls) and four
pages under `src/pages/sharedaccess/`:

- `SharedAccessHome.tsx` - tabbed (My wallets / Create group), folding
  the original's separate `landing.tsx` and `add.tsx` into one page, the
  same simplification §19's `FundWallet.tsx`/`TokenizeHome.tsx` already
  established for this app's own multi-tab pages.
- `GroupDetail.tsx` - a group's balance (native + curated tokens),
  member list, a propose-payment form, and inline add/remove-member,
  change-threshold and disable-group forms - folding the original's
  separate `walletInfo.tsx` and `update.tsx` into one page for the same
  reason: member-management proposals go through the exact same
  propose/approve/execute pipeline as a payment (`wallet-backend`
  PLAN.md §13.10 Phase 5), so there's nothing structurally different
  about "update" that would justify its own route once both are
  on-screen together. Proposing here immediately attempts a
  self-approval (build → `getAction` for the digest → `signHexDigest`
  → `approveAction`, the same build→sign→submit shape as `Send.tsx`) -
  meaningful for a 1-of-1 group or when the proposer alone satisfies the
  threshold; a genuine multi-party proposal still needs the other
  members' own approvals via the Approvals queue regardless.
- `Approvals.tsx` / `ApprovalDetail.tsx` - the original's
  `approvals.tsx`/`approvalDetails.tsx`, largely unchanged in shape:
  a queue of every pending action visible to the caller across every
  group they belong to, and a detail view to approve (sign the exact
  digest `getAction` returns) or reject (with a reason) one action.

Every `sharedAccessApi.ts` call passes the caller's own `primaryAddress`
as `walletAddress`, never a group's own address - unlike
`paymentsApi.ts`/`tokenizationApi.ts`, which act *as* a specific wallet
they name, `sharedaccess`'s own routes always identify the target group
by its own `:groupId` path/body param and authenticate the caller as
themselves (`wallet-backend`'s `signature_auth.go` "wallet == signer's
own owned wallet" fast path - see PLAN.md §13's `GroupMember.MemberAddress`
note: it's always a participant's *primary-wallet* address, resolved
through nested EIP-1271, never their raw signer key).

Routes added: `/shared-access`, `/shared-access/groups/:groupId`,
`/shared-access/approvals`, `/shared-access/approvals/:actionId`, with a
"Shared access" link in `AppLayout.tsx`'s nav bar.

**Verified**: `tsc -b && vite build` clean. Live E2E against a locally
booted `wallet-backend` (matching §19's own methodology): onboarded a
fresh wallet through to the dashboard, opened Shared access (My
wallets tab correctly listing the primary wallet as "OWNER"), and
submitted the Create group form - `CreateGroup` correctly attempted a
real Safe deployment and the error it returned ("failed to deploy group
wallet: fetch nonce: Post https://sepolia.base.org: Forbidden")
surfaced cleanly in the form, the same sandboxed-egress limitation
noted throughout this project's live-testing sessions (e.g. §16, §19),
not a code defect - confirms the request reaches the real backend
endpoint and the error path renders correctly. Approvals then correctly
showed "No pending actions" (the group was never created, so nothing
to show) - screenshots confirm real rendering against the live backend
throughout, not just a clean build.
