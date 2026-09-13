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

## 3. Signer vs. Primary Wallet — the two-mnemonic model

`wallet-backend`'s current `Register` (`internal/components/users/
services/services.go:60-114`) always sets the SIWE-verified address as
*both* `User.Address` (the signer/login identity) *and* the sole
`UserWallet` row, flagged `IsPrimary: true`. There is no existing path to
register with a signer different from the primary wallet — `RegisterWallet`
(`services.go:176-185`) only adds *additional*, non-primary addresses.

To honor a genuinely separate signer/primary-wallet import, `wallet-web`
needs one small, explicit `wallet-backend` addition — flagged here as a
required coordination change, the same way `wallet-payment-history-
engine/PLAN.md` §6 flagged its own `wallet-backend` dependency, not
implemented by this plan:

- **`POST /users/wallets/link-primary`** (authed as the signer): body
  `{address, signature, message}`, where `message` is a domain-bound,
  nonce-scoped statement ("Link {address} as the primary wallet for
  signer {signerAddress}. Nonce: {nonce}. Expires: {ts}.") signed by the
  **primary wallet's own key** — proving control of that address without
  ever transmitting or storing its private key, mirroring the exact
  nonce-issue/consume and `ecrecover`-based verification `auth/services/
  services.go` already implements for SIWE. On success, the service sets
  (or replaces) the `IsPrimary` `UserWallet` row for that user to the
  linked address, decoupling it from `User.Address`.

Until that lands, `wallet-web`'s import flow supports two modes:

1. **Same mnemonic for both** (default, and the only mode that works
   against `wallet-backend` unmodified today): one mnemonic derives one
   keypair, used both to SIWE-sign-in and to sign payments/swaps.
2. **Separate mnemonics**: the signer mnemonic registers/logs in as
   today; the primary-wallet mnemonic's address and a link-signature are
   held ready to call `link-primary` the moment that endpoint exists.
   `wallet-web`'s own implementation does not block on this landing, but
   its UI and `wallet-core` API are both built for it from day one so no
   rework is needed later.

Either way, every subsequent payment/swap for the primary wallet's funds
is signed with the **primary wallet's** key, never the signer's — the
signer key only ever produces SIWE signatures and (per §8) future
shared-access approval signatures.

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
encrypt_and_store(role: "signer"|"primary", phrase: &str, password: &str) -> VaultRecord
unlock(role, password: &str) -> UnlockResult { address }   // decrypts into worker-local memory only
lock(role)                                                  // zeroizes the in-memory key immediately
sign_siwe_message(role: "signer", message: &str) -> HexSignature
sign_transaction(role: "signer"|"primary", unsignedTxJson: &str) -> HexRawSignedTx
sign_link_primary_message(message: &str) -> HexSignature    // §3's link-primary proof, signed by the primary-wallet key
wipe(role)                                                  // deletes that role's vault record entirely (remove wallet)
```

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
worker-side): one record per role (`signer`, `primary`), written and read
*only* from within the Worker (§4.1) — the main thread never touches
IndexedDB for vault data directly, only asks the worker to.

Each `VaultRecord`:

```
{
  role: "signer" | "primary",
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

### 6.3 Transaction signing flow (payments/swaps)

1. UI calls `wallet-backend`'s `POST /payments/build` (or `/swaps/
   approve/build`) → gets back a `network.UnsignedTx` JSON.
2. UI passes that JSON to `walletCoreClient.signTransaction(role,
   unsignedTxJson)`.
3. The Worker's `wallet-core` instance (already unlocked for that role,
   or the UI prompts for the password first if not) signs it and returns
   only the raw signed transaction hex.
4. UI calls `POST /payments/submit` (or `/swaps/approve/submit`) with the
   signed hex. `wallet-backend` broadcasts it; `wallet-payment-history-
   engine` picks up the resulting on-chain event independently.

At no point does any signed-transaction payload, mnemonic, or private key
touch `wallet-backend` — only addresses, unsigned tx requests, and signed
tx hex ever cross the network boundary, in either direction.

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
| Malicious page script abusing a legitimate unlocked session | Narrow, fixed WASM API surface (§4.2) — no generic "sign arbitrary bytes" primitive exposed beyond the specific SIWE/tx/link-primary message shapes the app itself constructs |
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
