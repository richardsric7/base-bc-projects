# wallet-mobile: Base port of the original Flutter app

**Status: design only, nothing implemented yet.** This document is a plan,
written and pushed for review before any code is written, following the
same discipline used across `wallet-backend`, `wallet-web`, and
`wallet-payment-history-engine`: write the plan, push it, wait for
explicit authorization, then build.

This app is the mobile counterpart to `wallet-web` - same backend
(`wallet-backend`), same visual identity as the original
`trovo-wallet-monorepo/mobile` app, same business logic wherever Base's
blockchain model allows it to survive unchanged, ported wherever it
doesn't (sub-wallets, shared access, servicelinks, wallet recovery - see
`wallet-backend/PLAN.md` §12-§15, which this plan depends on throughout).

## 1. Source audit summary (what the original actually is)

Audited directly (not assumed) from `trovo-wallet-monorepo/mobile`:

- **Framework**: Flutter/Dart (`pubspec.yaml`, `android/`, `ios/`) - not
  React Native, not a native-per-platform app.
- **Chain SDK**: `stellar_flutter_sdk` is the only crypto/keypair
  dependency - all mnemonic derivation, keypair generation, and
  transaction/HTTP-request signing goes through it.
- **State management**: `provider` (a single large `DataProvider
  extends ChangeNotifier` in `storage/state.dart`, 911 lines) actually
  carries the app's state; `flutter_bloc` and `get` are also declared
  dependencies but not meaningfully used for state - vestigial.
- **Storage**: `sembast` (an unencrypted, file-based NoSQL store) holds
  **everything**, including the mnemonic-derived secret keys, in
  **plaintext** - no OS Keychain/Keystore, no `flutter_secure_storage`,
  no field-level encryption. The local "password" gate
  (`change_password.dart`) is a plaintext string compared in the UI
  layer, not a key used to encrypt anything. This is a real security gap
  in the original, not a design worth reproducing - §4 fixes it.
- **Custom Router 2.0** navigation (`router/`), not `go_router` or named
  routes - a `PageAction`/`PageState` enum drives push/replace across a
  central `RouterDelegate`.
- **Theme**: `custom_bloc_observer/colors.dart` (raw palette:
  `trovoblue = 0xFF00225A` + opacity ramp, `darkblue`, semantic
  `green`/`upcomingassetyellow`/`grey`, tier colors, and pink/green/orange
  6-step ramps), `notifire_clor.dart` (`ColorNotifier`, the actual
  light/dark theme abstraction every screen reads through), `fonts.dart`
  (Montserrat/Gilroy/Matahari families, more fonts bundled in
  `pubspec.yaml` than the 2 constants file admits to).
- **The closest thing to a "core"**: `functions/trovo-sdk.dart`
  (`TrovoWalletSDK`) - a thin wrapper over `stellar_flutter_sdk` covering
  exactly five operations: keypair generation, mnemonic derivation/import,
  HTTP-request signing, and XDR transaction signing. Everything else
  (header assembly, the two-phase sub-wallet/shared-access sign-and-submit
  dance, the deep-link action dispatcher) is embedded directly in screen
  and state files, not factored out.
- **QR/deep-link dispatch**: `storage/state.dart`'s `processDeepLink` is
  the single switch on a decoded link's `action` query parameter -
  `login`/`payment`/`authorize`/`register`/`tokenizedAsset` are handled;
  there is **no `'event'` case**, despite the backend having a real
  `EVENT` servicelinks kind (`wallet-backend/PLAN.md` §14.2 item 5) -
  an original-app gap, not something to carry forward.

## 2. Framework decision

**Recommendation: stay on Flutter.** The explicit ask is to reuse the
original's theme and business logic as closely as Base allows - Flutter
lets the actual theme file (`colors.dart`, `notifire_clor.dart`,
`fonts.dart`) and most reusable widgets (`custtom_button.dart`,
`custom_app_bar.dart`, textfields) be ported with minimal rewriting,
since it's the same language and widget model. Rewriting in React Native
to match `wallet-web`'s TS/React stack would mean re-deriving every
screen's layout from scratch for no benefit this project needs - the
mobile and web apps don't share UI code today in the original either, and
don't need to here.

## 3. Directory layout (mirrors the original's `lib/` structure)

```
wallet-mobile/
  lib/
    main.dart                    # bootstrap, MultiProvider (DataProvider, ColorNotifier)
    router/                      # ported as-is (page/route logic is chain-agnostic)
    theme/                       # was custom_bloc_observer/ - colors, fonts, ColorNotifier
    screens/
      auth/                      # registration/import - mnemonic UI, no XDR-signing step
      account_recovery/
      subwallets/                # was folded into bottom_bar/all_wallets.dart - simplified, see §7
      shared_access/
      servicelinks/              # was screens/auth/authorize_*_view.dart - QR + approval screens
      send_and_receive/
      swap/
      asset_tokenization/
      subscriptions/
      kyc/
      backup/
      delete_account/
    bottom_bar/                  # tab shell + bottom_pages/ (home, wallets, settings, ...)
    core_bridge/                 # generated FFI bindings to wallet-core (see §4) + thin Dart wrappers
    network/                     # httpClient equivalent - request signing, base URL selection
    storage/                     # DataProvider (app state), secure-storage-backed cache (see §4.3)
    models/                      # plain Dart data classes, ported field-for-field where the schema matches
    widgets/                     # shared UI (buttons, popups, secret-reveal, loaders)
  android/ ios/                  # stock Flutter scaffolding
```

## 4. `wallet-core` extraction: what moves to the shared Rust crate

`wallet-web/wallet-core` already exists (built in that project's own
Phase 106-109) with exactly the primitives a mobile client needs:
`generate_mnemonic`/`validate_mnemonic`/`derive_preview_address`
(`mnemonic.rs`), `encrypt_vault`/`unlock`/`lock`/`lock_all`/`is_unlocked`
(`vault.rs`, an encrypted-at-rest vault keyed by password, one unlocked
role's key ever held in memory, zeroized on lock), and
`sign_request_message`/`sign_hex_digest`/`sign_transaction` (`signing.rs`). This is real,
working code already exposed via `wasm-bindgen` for the browser - the
task here is exposing the *same* crate to Dart, not writing a second
implementation.

**Only one role/key exists: the signer** - `wallet-web/PLAN.md` §3 was
corrected to drop a "primary wallet" mnemonic/role and its
`sign_link_primary_message` function entirely, once `wallet-backend/
PLAN.md` §13 made the primary wallet a Safe smart-contract account with
no private key of its own (needed for §15's wallet-recovery feature -
recovering a lost signer means *replacing* the Safe's owner, which only
makes sense if the wallet is a Safe rather than a bare EOA). The same
correction applies here unchanged: this app only ever generates, stores,
and unlocks one mnemonic per user, and the primary wallet's address is
computed (CREATE2), not imported.

### 4.1 FFI binding tool

**Recommendation: [`flutter_rust_bridge`](https://cjycode.com/flutter_rust_bridge/)**
over raw `uniffi` - it generates idiomatic Dart bindings directly from
the Rust source (including `async`, `Result`, and struct types) with
less boilerplate than uniffi's UDL-file approach, and is the
Flutter-specific tool of choice for exactly this "one Rust core, one
Flutter app" shape. This adds a `flutter_rust_bridge` codegen step to
`wallet-core`'s build (alongside the existing `wasm-pack` build for
`wallet-web`) - the crate itself doesn't fork, it grows a second binding
target.

### 4.2 What's reused unchanged vs. what needs adding

| Function | Status |
|---|---|
| `generate_mnemonic`, `validate_mnemonic`, `derive_preview_address` | Reuse as-is |
| `encrypt_vault`, `unlock`, `lock`, `lock_all`, `is_unlocked` | Reuse as-is - this becomes mobile's answer to the original's plaintext-sembast problem (§4.3) |
| `sign_request_message` | Already renamed from `sign_siwe_message` (wallet-core/wallet-web PLAN.md §11 - it was always message-agnostic EIP-191 `personal_sign`, so this was a rename only, no behavior change). Used **only** for genuinely human-composed string messages - the per-request SignatureAuth header (`path + signer + timestamp`). **Do not** reuse this for a hex-encoded digest (see the next row) - `wallet-web` did exactly that and it shipped a real bug, corrected in `wallet-web/PLAN.md` §15. |
| `sign_hex_digest` | **New**, added by `wallet-web/PLAN.md` §15's fix. Signs a `0x`-prefixed hex digest (a Safe transaction hash - `sharedaccess.DigestToSign`/wallet-recovery's `*SafeTxHash` fields, `wallet-backend/PLAN.md` §13/§15/§17) by hex-decoding it to raw bytes *first*, then applying EIP-191 `personal_sign` to those raw bytes - required because `wallet-backend`'s actual verification (`cryptoutil.VerifyPersonalSignBytes(digest.Bytes(), ...)`) hashes the raw bytes with their own (32-byte) length prefix, not the hex string's (66-character) one. This is the function every payment, swap, asset-approval, and wallet-recovery enable/disable approval on this app must use - `sign_request_message` would produce a validly-formed but silently wrong signature. |
| `sign_transaction` (EIP-1559) | Narrower role than originally scoped - kept for any plain-EOA signing need, but no longer how primary-wallet or sub-wallet payments/swaps are authorized (see above) |

### 4.3 Fixing the original's plaintext-storage gap, not reproducing it

The original stores secret keys in cleartext in an unencrypted local DB
(§1). `wallet-core`'s vault (`vault.rs`) already solves this correctly
for `wallet-web` - reuse it directly for mobile instead of introducing a
second, Flutter-only encryption scheme: `encrypt_vault`'s output
(`VaultRecord` JSON) gets persisted via `flutter_secure_storage`
(OS Keychain on iOS, Keystore-backed on Android) instead of raw
`SharedPreferences`/sembast, so the file on disk is already ciphertext
*and* sits behind the OS's own access control. Local biometric/password
gating (`local_auth`, already a dependency) becomes the *unlock* step
that decrypts the vault, not a cosmetic UI check the way the original's
plaintext-password comparison is today.

## 5. Networking layer

Ports `network/requests.dart`'s `getRequestHeader` pattern onto the new
scheme once `wallet-backend/PLAN.md` §12 lands: build
`fullPathWithQuery + signerAddress + timestamp`, sign it via
`wallet-core`'s (possibly renamed) request-signing function, attach
`X-Signer-Address`/`X-Wallet-Address`/`X-Signature`/`X-Timestamp` (the
finalized names from `wallet-backend/PLAN.md` §12.2) instead of the
original's `X-TW-*` headers. Device-ID/app-version headers
(`functions/helpers.dart`) carry over unchanged - they're not
chain-specific.

### 5.1 Environment configuration: a testnet/mainnet switch, mirroring `wallet-web`

`wallet-backend` is deployed once per network (separate services,
separate databases - never one server for both chains, see
`wallet-backend/DEPLOYMENT.md` §1/§9), so "switching network" here means
"point the app at a different `wallet-backend` deployment," exactly as
`wallet-web/PLAN.md` §10's now-implemented in-app switch
(`src/config/network.ts`) does. Port the same design rather than
inventing a second one:

- Two named environments, each a `(backendUrl, chainId, label)` triple -
  `Base Sepolia (Testnet)` and `Base Mainnet` - baked into the app at
  build time via Flutter's `--dart-define` (the Dart/Flutter equivalent
  of Vite's build-time `VITE_*` vars; there is no `.env` file read at
  runtime in a compiled mobile app). Define both in every build:
  `TESTNET_BACKEND_URL`/`TESTNET_CHAIN_ID` and
  `MAINNET_BACKEND_URL`/`MAINNET_CHAIN_ID`, read via
  `String.fromEnvironment`/`int.fromEnvironment` at startup into a single
  `NetworkConfig` held in app state (`provider`/whatever this settles
  on, per §12's open dependency question) - not scattered `Platform`
  lookups at each call site.
- The **active** network is a runtime choice persisted in local device
  storage (`shared_preferences` is sufficient - this is a non-secret UI
  preference, unlike the vault, which stays in `flutter_secure_storage`
  per §4.3), defaulting to testnet for the same forgotten-config-safety
  reason `wallet-backend`'s own `BASE_CHAIN_ID` and `wallet-web`'s
  `VITE_DEFAULT_NETWORK` both default to Sepolia/testnet.
- A Settings screen toggle (ported alongside the existing settings
  screens in Phase 5/9's dashboard work) switches it, mirroring
  `wallet-web`'s Settings-page toggle: switching networks should restart
  the app's navigation stack back to the root/splash screen rather than
  attempting to reconcile in-memory state, since testnet and mainnet are
  separate `wallet-backend` deployments with entirely separate user
  registrations - there is nothing to translate from one to the other.
  Disable (grey out) whichever network's backend URL define was left
  empty, so a build that only configured one network can't let someone
  select the other into a dead endpoint.
- This is orthogonal to §5's signature-auth header work above - the
  network switch changes which backend/chain the app targets, not what
  auth scheme it speaks to that backend once selected.

## 6. Sub-wallets: a simpler flow than the original's

Per `wallet-backend/PLAN.md` §13.3, creating a sub-wallet on Base needs
**no client-side keypair generation and no second co-signing step** -
the original's `all_wallets.dart` `generateKeyPairs()` →
`sendDataToServer()` → sign-twice → `sendFullDataToServer()` dance
collapses to: submit a signed creation request (tag/description only),
receive back the computed Safe address immediately. The `subwallets/`
screen group is simplified accordingly rather than ported line-for-line -
there's no "sign this XDR with your new sub-wallet's key" step to build a
screen for, because that key never exists.

## 7. Shared access: ported UI, new signing shape

Screens `add_shared_access_details.dart`, `approval_details.dart`,
`shared_wallet_info.dart`, `viewer_access.dart` port with their
role-based logic intact (`VIEW-ONLY`/`INITIATOR`/`APPROVER` -
`wallet-backend/PLAN.md` §13.8 leaves the option to rename the third
role to `AUTHORIZER` open, cosmetic either way). What changes underneath:
approving a pending action signs a `SafeTxHash` via `sign_hex_digest`
(§4.2 - plain EIP-191 `personal_sign` over the digest's raw bytes, not
EIP-712 typed-data signing as this section previously assumed), not an
XDR co-signature via `signBase64Txn`; balance/asset
views read the new curated-asset-filtered summary endpoint
(`wallet-backend/PLAN.md` §13.8) instead of an unfiltered Horizon balance
list.

## 8. Servicelinks: QR scanning, login/authorize/payment retained, the missing `EVENT` case added

`qr_scanner_view.dart` and `processDeepLink`'s action-dispatch pattern
port as the right shape - scan a link (now pointing at the reused
`shortlink` component's QR output, `wallet-backend/PLAN.md` §14.2 item
2, rather than Firebase Dynamic Links), decode its `action` parameter,
push the matching screen. Every action the original's dispatcher already
handles carries over:

- **`login`** - pushes the same login-approval screen; verification still
  yields the partner a session token exactly as today (`wallet-backend/
  PLAN.md` §12.5/§14's login-verification row - unchanged behavior, just
  confirmed explicitly here since it's easy to assume "redesign" implies
  this path changed too).
- **`authorize`** - the 2FA/generic-approval screen, unchanged shape
  (no token issued on verify, per the same table).
- **`payment`** - **not an approval screen at all** (`wallet-backend/
  PLAN.md` §14.1a) - decodes straight into the ordinary send-payment
  screen, pre-filled from the link's `to`/`tokenAddress`/`amount`/`memo`
  query parameters, the same way the original pre-fills its send screen
  from `paymentDestination`/`assetCode`/`assetIssuer`/`amount`/`memo`.
  The user reviews and signs it like any other payment - there is no
  separate "authorize this payment-request" step to build, because the
  original doesn't have one either (§14.1a).
- **`event`** (new) - the case the original's own mobile app never wired
  up despite the backend supporting it (§1, `wallet-backend/PLAN.md`
  §14.2 item 5); this port adds its own approval screen alongside the
  others, closing a gap in the *original*, not introducing a
  Base-specific one.
- `register`/`tokenizedAsset` carry over as their own existing flows,
  unaffected by this redesign.

## 9. Wallet recovery: screens and flow for both branches

The original's `screens/account_recovery/` (§1) covers what
`wallet-backend/PLAN.md` §15 splits into **two coexisting branches**,
presented to the user as genuinely different options, not one an
upgrade of the other:

- **Branch A - free, DB-only address swap** (already built
  server-side, `recovery.go`): the account gets re-pointed to a fresh
  address; whatever was at the old one - funds, sub-wallets, shared-access
  memberships - is abandoned, not carried over. No enrollment screen
  beyond a toggle - it depends on the same security questions likely
  already set up at registration.
- **Branch B - paid, true wallet recovery** (new, §15.5): same address,
  same sub-wallets, same shared-access memberships afterward - a Safe
  owner-swap on the primary wallet, nothing else. Needs its own
  enable/disable settings screen: security-question setup if not already
  done, a plain-language explanation of what the recovery service can
  and cannot do (especially worth advertising if `wallet-backend/PLAN.md`
  §15.3's recommended Safe Guard - restricting the recovery service to
  owner-management calls only, never a direct transfer - is
  implemented), and the one-off fee disclosure.

Both branches share **one recovery execution flow, reachable without
being logged in**, since by definition the user has no working signer
key: security questions, email OTP, then `wallet-core` generates a
**fresh** mnemonic/vault right there in this flow (before any successful
login exists) and produces a `personal_sign` proof from that new key
(`wallet-backend/PLAN.md` §15.5 step 1) - only the last step (which
branch's endpoint gets called) differs, and the screen should say
plainly what each choice preserves versus abandons before the user
picks. For Branch B specifically, per §15.4 of that document, nothing
about sub-wallets or shared-access memberships needs a "select which
wallets to recover" step - the backend's nested-ownership design means
swapping the primary wallet's signer is the only on-chain change that
ever happens, unlike what the original's own multi-wallet-loop behavior
might suggest.

## 10. Business-logic parity map

| Original (Dart/Flutter, Stellar) | wallet-mobile (Dart/Flutter, Base) | Where it lives |
|---|---|---|
| `TrovoWalletSDK.createAccount`/`generateCredentialsFromPassPhrase` | mnemonic generation/derivation | `wallet-core` (FFI) |
| `TrovoWalletSDK.signHTTP` | per-request signature (§5) | `wallet-core` (FFI) |
| `TrovoWalletSDK.signBase64Txn` | EIP-1559 tx signing / `sign_hex_digest` for Safe approvals (§4.2) | `wallet-core` (FFI) |
| Sembast plaintext key storage | encrypted vault + OS Keychain/Keystore (§4.3) | `wallet-core` + `flutter_secure_storage` |
| Sub-wallet two-phase create+co-sign | single signed create request (§6) | app screen + `wallet-backend` |
| Shared-access grant/approve screens | same screens, `sign_hex_digest` approval signing | app screen + `wallet-core` |
| `processDeepLink` action switch | same switch, `+event` case, new QR source | app (`storage/`) |
| Theme (`custom_bloc_observer/`) | ported palette/typography, same token names where practical | app (`theme/`) |
| `screens/account_recovery/` | wallet-signer recovery (§9) | app screen + `wallet-core` + `wallet-backend` §15 |

## 11. Cross-project dependencies (tracked here, resolved elsewhere)

- Needs `wallet-backend/PLAN.md` §12 (per-request signature auth), §13
  (Safe-based sub-wallets/shared-access, now covering the primary wallet
  too), §14 (servicelinks QR), and §15 (wallet recovery) implemented and
  stable before this app's networking/sub-wallet/shared-access/
  servicelinks/recovery layers can be built against real endpoints.
- Needs `wallet-core` to grow: FFI bindings (§4.1) exposing the crate's
  existing `sign_hex_digest` (already added and shipped for `wallet-web`,
  `wallet-web/PLAN.md` §15 - not a new addition to build here, just a new
  binding target for an existing function).

## 12. Open decisions

- `flutter_rust_bridge` vs `uniffi` for the FFI layer (§4.1 recommends
  the former).
- Whether to drop the unused `flutter_bloc`/`get` dependencies entirely
  and standardize on `provider` alone (recommended - they carry no real
  state in the original either), or keep them for parity with the
  original's dependency list.
- `APPROVER` vs `AUTHORIZER` naming, shared with `wallet-backend/
  PLAN.md` §13.8/§13.9.

## 13. Phased build roadmap

| Phase | Scope | Depends on | Status |
|---|---|---|---|
| 1 | `wallet-core`: FFI bindings (§4.1) for the existing crate, including `sign_hex_digest` (§4.2) | — | **Done** - see §14, using a hand-rolled `dart:ffi` bridge instead of `flutter_rust_bridge` (§14.1 explains the change) |
| 2 | App scaffold: Flutter project, ported theme (§3, colors/fonts/`ColorNotifier`), router port | Phase 1 | **Done** |
| 3 | Vault + secure storage (§4.3), onboarding/import screens | Phase 1, 2 | **Done** |
| 4 | Networking layer with the finalized signature-auth headers (§5) and the testnet/mainnet environment switch (§5.1) | `wallet-backend` §12 shipped | **Done** |
| 5 | Dashboard, send/receive, swap - straightforward ports | Phase 4 | **Partial** - dashboard and send done; swap not ported (§14.2) |
| 6 | Sub-wallets (§6), shared access (§7) | `wallet-backend` §13 shipped | Not started |
| 7 | Servicelinks QR scanning + approval screens, including the new `EVENT` case (§8) | `wallet-backend` §14 shipped | Not started |
| 8 | Wallet recovery enable/disable + execution screens (§9) | `wallet-backend` §15 shipped | Not started |
| 9 | KYC, subscriptions, asset tokenization, backup/delete-account | Phase 5 | Not started |
| 10 | Full integration pass against Base Sepolia; push | Everything above | Not started |

## 14. Implementation status (Phases 1-4, partial 5)

This app is no longer design-only. Phases 1-4 and the dashboard/send
half of Phase 5 are implemented, tested, and visually verified. What
follows records what was actually built, where it diverges from this
plan's original design, and exactly what's left.

### 14.1 FFI: hand-rolled `dart:ffi`, not `flutter_rust_bridge`

§4.1 recommended `flutter_rust_bridge`. In practice a plain C-ABI bridge
was used instead: `wallet-web/wallet-core/src/ffi.rs` adds 12
`#[no_mangle] pub extern "C" fn` exports (mnemonic generate/validate/
derive, vault encrypt/unlock/lock/lock_all/is_unlocked, and the three
signing functions from §4.2) alongside the crate's existing
`wasm-bindgen` exports - `Cargo.toml`'s `crate-type = ["cdylib", "rlib"]`
now produces both the WASM build (`wasm-pack`, for `wallet-web`) and a
plain native `.so`/`.dylib`/`.dll` (`cargo build`, for `wallet-mobile`)
from the same source tree. Each fallible call returns a JSON envelope
(`{"ok":bool,"value":..,"error":..}`) through an owned C string the
caller must free (`ffi_free_string`) - this is simpler than it sounds
because Dart's side (`lib/core/wallet_core_bindings.dart`,
`wallet_core_client.dart`) wraps every call in one `_callEnveloped`
helper that decodes the envelope and frees the string in a `finally`.

Reasoning for the change: `flutter_rust_bridge` needs its codegen
tool wired into the build for a platform-specific Flutter toolchain,
and its main benefit (typed async/Result/struct bindings) isn't needed
here - the function surface is small (12 functions, all synchronous,
all string-in/string-out) and this crate already has a JSON-envelope
convention from its `wasm-bindgen` side that a hand-rolled bridge could
reuse directly. This also kept the same underlying `.so` file
independently testable via `flutter test` on the Dart VM (which loads a
real native library on Linux, not a mock) - see §14.4.

Session-state note: `FFI_SESSION` (`ffi.rs`) is a `thread_local!` inside
the Rust library, which for a same-process embedding is effectively
one shared table for the whole app (mirroring the existing
`wasm-bindgen` module's single in-memory session). This matches the
original design intent (one signer role unlocked at a time, `lock_all`
clears it) but is worth keeping in mind if mobile ever needs isolates.

### 14.2 What's built

- **Theme** (`lib/theme/app_theme.dart`): colors and font families
  ported verbatim from `wallet-web/app/tailwind.config.js`; the 5 font
  files copied into `assets/fonts/` and registered in `pubspec.yaml`.
- **Network config** (`lib/config/network.dart`): testnet/mainnet
  switch via `String.fromEnvironment`/`--dart-define`, the mobile
  equivalent of `wallet-web`'s Vite `import.meta.env.VITE_*` (§5.1).
- **Offline cache** (`lib/store/offline_cache.dart`) and **vault
  storage** (`lib/core/vault_storage.dart`, on top of
  `flutter_secure_storage` for the OS Keychain/Keystore) - the non-secret
  and secret persistence halves, matching `wallet-web`'s split.
- **API clients** (`lib/api/`): `http_client.dart` (SignatureAuth
  headers per §5), `users_api.dart`, `assets_api.dart`,
  `payments_api.dart` - ported from `wallet-web`'s equivalents,
  including `withWalletDeployRetry` and the `ActionProposal`/
  `digestToSign` approval shape from `wallet-backend` §13.
- **State** (`lib/store/wallet_state.dart`): a `ChangeNotifier`-based
  `WalletState`/`RoleState`, per §2's `provider` recommendation.
- **Screens**: onboarding wizard (create/import, mnemonic reveal,
  password set), unlock, dashboard, send, and a simplified settings
  page (wallet addresses, network label, wipe-device danger zone).
  Swap was **not** ported in this pass - Phase 5 is only partially
  done.
- **Not ported this pass, tracked as follow-ups**: swap screen; a
  recovery entry point on the unlock screen (`unlock_page.dart` has a
  `TODO(wallet-mobile)` marking exactly where `wallet-web`'s
  `RecoveryWizard`, §9, would plug in); Phases 6-10 in full
  (sub-wallets, shared access, servicelinks, wallet recovery, KYC/
  subscriptions/tokenization/backup).

### 14.3 Open decision resolved

§12 left `flutter_bloc`/`get` vs. `provider` open. Resolved in favor of
`provider` alone, per that section's own recommendation - neither of
the other two packages was added.

### 14.4 How this was verified without a device or emulator

This sandbox has no Android/iOS emulator, no GUI, and no browser
dedicated to Flutter. Two of Flutter's own headless mechanisms cover
everything short of on-device testing:

- **Native FFI correctness**: `flutter test` runs Dart on the Dart VM,
  which runs natively on Linux and can `dlopen` a real compiled
  `.so` - `test/core/wallet_core_client_test.dart` (7 tests) exercises
  the actual `libwallet_core.so` (via `WALLET_CORE_LIB_PATH`), not a
  mock: mnemonic generate/validate/derive, `createVault` -> `lock` ->
  `unlock` round-trips to the same address, wrong-password rejection,
  and `signHexDigest` vs. `signRequestMessage` producing different
  (both valid) signatures. `wallet-core`'s own Rust test module
  (`ffi.rs`) adds 3 more round-trip tests at the Rust level.
- **Visual verification**: Flutter's golden-screenshot testing
  (`matchesGoldenFile`) renders real PNGs through Flutter's software
  rendering pipeline, viewable directly - no emulator or browser
  needed. `test/goldens/onboarding_choose_signer.png` and
  `test/goldens/send_page_idle.png` are checked-in golden references,
  visually confirmed to render correctly (button/border colors, layout,
  font). A third golden for the mnemonic-reveal screen was deliberately
  **not** kept as a pixel-diff assertion: `generate_mnemonic()` returns
  a fresh random phrase on every run, so that screen's text (and every
  pixel) legitimately differs run to run - a fixed golden there would
  be flaky by construction. That screen's test instead asserts its
  structure (a "Your recovery phrase" heading and 12 numbered words)
  and was visually spot-checked once by hand.
- **Not verifiable in this sandbox**: real Android/iOS packaging
  (placing the native library under `jniLibs`/using
  `DynamicLibrary.process()` via static linking on iOS, per
  `wallet_core_bindings.dart`'s `_open()`), platform channels, and any
  on-device behavior. This needs Android Studio + an emulator, or Xcode
  on macOS - outside any environment currently available to this
  session.

`flutter analyze` is clean (0 issues) and all 10 Dart tests pass
(`flutter test`), alongside `wallet-core`'s existing Rust test suite.

### 14.5 Real theme and brand artifacts, sourced from `trovo-wallet-monorepo/mobile`

§14.1-14.4 above ported wallet-web's own tailwind-derived theme
(`AppColors`/`AppFonts` in `theme/app_theme.dart`), since at the time
this app had no color/logo assets of its own. `trovo-wallet-monorepo`
(a separate, read-only reference repository - never modified by this
project, per explicit instruction) is the actual original production
Flutter app this whole `wallet-mobile` effort is porting from, and its
`lib/custom_bloc_observer/colors.dart` and `assets/images/` are the real
source of truth for the brand, so this pass reconciled the two:

- **Colors**: comparing `colors.dart` against the existing
  `AppColors` scale showed `primary100`-`primary700` were already
  byte-for-byte identical to the real app's `trovoblue50`-`trovoblue90`
  tints - wallet-web's tailwind config had evidently been built from the
  same source at some point. Only `primary800` had drifted (wallet-web's
  invented `0xFF004988` vs. the real app's actual brand navy,
  `trovoblue` = `0xFF00225A`) - corrected here. The positive/success
  green (`positiveLight`/`positivePrimary`, used for the online/offline
  indicator - PLAN.md §6.4 - which wallet-web had invented outright since
  its own theme had no success color to port) was replaced with the real
  app's own `green`/`colorGreen60` (`0xFF00A859` / `0xFFE6FBF1`).
- **Artifacts**: `trovo_app.png` (the color wordmark) and `trovo_white.png`
  (the white mark, for use on colored backgrounds) were copied byte-for-
  byte from the real app's `assets/images/` into this app's own
  `assets/images/` (a one-time copy, not a shared/linked path - the two
  repos remain otherwise unrelated). `trovo_app.png` replaces the plain
  "Trovo Wallet" text that previously stood in for a logo atop
  `AuthLayout` (used by onboarding and unlock); `trovo_white.png` sits on
  a `trovoblue`-colored `DrawerHeader` in the dashboard's navigation
  drawer, mirroring how a colored nav header with a white mark is used
  throughout the real app.
- **A real decode bug found and fixed along the way**: `trovo_app.png`
  carried an embedded custom ICC color-profile chunk (`iCCP`). Once
  wired into a widget via `Image.asset`, this sandbox's headless
  `flutter_tester` engine decoded the image but only after several
  minutes of real wall-clock time (observed directly with an isolated
  `ui.instantiateImageCodec` probe) - far slower than any widget test's
  settle window, so the image silently rendered as blank space in every
  test that used it. The ICC chunk was stripped by re-encoding the PNG
  through Pillow (pixel data byte-identical, confirmed by direct visual
  comparison) as the fix for the file itself, but the underlying
  Flutter-test gotcha is separate and applies to *any* `Image.asset` in
  a golden test regardless of the file: `testWidgets`' fake-async test
  zone advances fake timers but never drains genuine platform-channel/
  native-codec I/O, so `pumpAndSettle()` alone cannot wait for a real
  image decode to finish. `test/pages/onboarding_wizard_test.dart`'s
  choose-signer golden and the new `test/pages/dashboard_page_test.dart`
  both now call `precacheImage(...)` inside `tester.runAsync(...)`
  before pumping, which escapes the fake-async zone and lets the decode
  actually complete - the documented, standard fix for this class of
  problem. Both goldens now show the real multi-color logo pixels
  (confirmed by direct pixel sampling of the PNG, not just visual
  inspection) rather than blank space.
- **New test**: `test/pages/dashboard_page_test.dart` (previously the
  dashboard/drawer had no test coverage at all) opens the drawer and
  golden-asserts `test/goldens/dashboard_drawer.png`, the first visual
  check of the drawer's brand header.

`flutter analyze` is clean and all 11 Dart tests pass after this pass
(10 from §14.4 plus the new dashboard drawer test); the two pre-existing
goldens (`onboarding_choose_signer.png`, `send_page_idle.png`) were
regenerated to reflect the corrected `primary800` and the real logo.

### 14.5 Alias/username-aware Send, mirroring `wallet-web`

`wallet-backend`'s `/v1/payments/build` resolves its recipient through
address/username/email/wallet alias (`users.UserWallet`'s own doc
comment) and returns what it resolved to as `resolvedAddress` -
`send_page.dart` only ever asked for a raw "Recipient address" and
never surfaced that field. Fixed to match `wallet-web`'s own §22 fix:
`payments_api.dart`'s `ActionProposal` gains `resolvedAddress`;
`send_page.dart`'s field is now "Recipient" with a hint describing all
four accepted forms, shows a "Sending to: X" confirmation once `/build`
responds, and passes the resolved address (not the raw identifier) to
`submitPayment` so a payment history record's `toAddress` is always a
real address. `app_text_input.dart` gained a `hint` parameter
(`InputDecoration.hintText`) to support this - it had none before.
`users_api.dart` also gains `UserWallet`/`listMyWallets`/
`updateWalletMetadata`/`WalletDirectoryEntry`/`resolveRecipient`,
mirroring `wallet-web`'s `usersApi.ts` additions, ready for a future
wallet-directory management screen - not built in this pass, since
mobile's own port is still intentionally partial (§14.2/§14.4: no
swap/shared-access/tokenize screens exist yet either) and a directory
screen with nothing else at parity around it would be scope creep
beyond what this correction pass needs.

**Verified**: `flutter analyze` clean (3 pre-existing-style `info`
suggestions on the new API methods, no warnings/errors); all 11 tests
pass, including `send_page_test.dart` after updating its "Recipient
address" text assertion to "Recipient" and regenerating
`send_page_idle.png`.

### 14.6 Full feature-parity close-out: swap, shared access, tokenization,
fund/receipt/wallets/recovery

§14.2's "not ported this pass" list is now closed out. Every remaining
`wallet-web` page (per its own `App.tsx` route table) has a mobile
counterpart, each following the same pattern used throughout this
port: read the `wallet-web` source in full, write a matching Dart API
client in `lib/api/`, write matching Dart page(s) under `lib/pages/`,
register any new API client on `AppServices`, wire routes into
`main.dart` (a static `routes` map entry for a fixed path, or an
`onGenerateRoute` branch for a parameterized one, since Flutter's named-
route map can't hold a dynamic segment), add a drawer entry where
`wallet-web` has an equivalent nav entry, `flutter analyze`, add a
widget test, `flutter test`, and regenerate any golden that shifted.

- **Swap** (`api/swaps_api.dart`, `pages/swap/swap_page.dart`): a
  generic router-call form (router address/ABI/method/args/value),
  mirroring `Swap.tsx`'s build -> `signHexDigest` -> submit flow
  exactly (same shape as Send's own approval flow, §13/§17).
- **Shared access** (`api/sharedaccess_api.dart`,
  `pages/sharedaccess/`): wallet listing (mine/shared-with-me, an
  owner-and-shared badge), group creation, group detail (balance,
  curated balances, propose payment, add/remove member by username,
  change threshold, disable group), an approvals inbox, and an
  approval detail screen (approve/reject with a signed digest) -
  the mobile counterpart of `sharedAccessApi.ts` and its five
  `pages/sharedaccess/*.tsx` screens (§7).
- **Tokenization** (`api/tokenization_api.dart`,
  `pages/tokenize/`): a 3-tab hub (my applications / new application /
  my investments) plus an asset detail screen handling both the
  issuer side (confirm application, confirm fee payment) and the
  investor side (subscribe with crypto, express interest, early
  exit), mirroring `tokenizationApi.ts`/`TokenizeHome.tsx`/
  `AssetDetail.tsx`. This lands in the same pass as the backend's own
  restricted-asset correction below, so the mobile subscribe flow
  already goes through the corrected buyer-KYC-gated, on-chain-
  authorized purchase path - no separate mobile-side change was
  needed for that fix, since authorization and KYC are enforced
  server-side in `BuildCryptoPurchase`/`BuildFiatPurchase`, not
  client-side.
- **Fund wallet** (`api/fiat_api.dart`, `api/stablerail_api.dart`,
  `api/crypto_api.dart`, `pages/fund/fund_wallet_page.dart`): the
  three funding rails as tabs (Flutterwave card/bank invoice, NGN/
  Stablerail BVN-onboarding-and-onramp, crypto deposit-address-and-
  withdrawal), mirroring `fiatApi.ts`/`stablerailApi.ts`/
  `cryptoApi.ts`/`FundWallet.tsx`. Withdrawal is the one path here
  that moves the user's own on-chain funds, so it alone follows
  build -> `signHexDigest` -> confirm; fiat/stablerail top-ups and
  crypto deposits never touch the signer key.
- **Receipt** (`pages/receipt/receipt_page.dart`): renders a
  `PaymentHistoryRecord`'s from/to/amount/tx-hash-with-explorer-link/
  date, mirroring `Receipt.tsx`. `wallet-web` reaches it via router
  state from `Send.tsx`'s success view or a `Dashboard.tsx` history
  row; there being no router-state layer on mobile, the record is
  simply passed as a constructor argument through a direct
  `Navigator.push` from the equivalent spot in `send_page.dart`
  (a new "View receipt" link after a successful send) and
  `dashboard_page.dart` (a new "Receipt" button per history row).
  `wallet-web`'s own browser-print-to-PDF button has no mobile
  equivalent and was not ported - a deliberate simplification (there
  is no comparable OS-level "print to PDF" affordance to invoke from
  within a Flutter app without adding a new dependency for one static
  document), not a missing feature; the fields shown are identical.
- **My Wallets** (`pages/wallets/my_wallets_page.dart`): lists the
  caller's own wallet directory and lets Tag/Description/Alias be
  edited in place, using `users_api.dart`'s already-ported
  `listMyWallets`/`updateWalletMetadata` (added back in §14.5 ahead of
  a screen to use them, exactly as anticipated there) - mirrors
  `MyWallets.tsx`.
- **Recovery** (`api/recovery_api.dart`,
  `pages/settings/recovery_settings_page.dart`,
  `pages/recovery/recovery_wizard_page.dart`): the owner-side settings
  screen (security-question answers, Branch A/B enable-disable, both
  gated behind the OTP-plus-security-answer factors wallet-backend
  PLAN.md §15 requires) is now linked from `settings_page.dart`
  (previously a bare stub with only wallet addresses/network/wipe);
  the wizard (username -> branch choice -> generate-and-confirm a new
  signer mnemonic -> OTP + security answers -> recover) replaces
  `unlock_page.dart`'s `TODO(wallet-mobile)` with a real "Lost access
  to your key?" entry point, reachable with no existing signer at all
  - the whole point of recovery. Mirrors `RecoverySettings.tsx`/
  `RecoveryWizard.tsx`/`recoveryApi.ts` exactly, including the
  signRequestMessage-vs-signHexDigest distinction each branch depends
  on (a human-composed `RecoveryMessage` for the new address's own
  proof-of-control signature; raw-digest signing for the Safe
  owner-swap SafeTxHashes in Branch B).

**Restricted tokenized-asset correction (backend, same pass, not a
mobile change)**: the user flagged that every tokenized asset is a
restricted asset - on Base this means an allow-list on the ERC-20
itself, the equivalent of Stellar's issuer `AUTH_REQUIRED` trustline
flag the original app relied on, which `TokenizedAsset.sol` had never
carried (a prior deliberate "plain ERC-20" simplification, now
overridden). Fixed in `wallet-backend`: the contract gained
`isAuthorized`/`authorize`/`deauthorize`, enforced in
`_beforeTokenTransfer`, with `mint` auto-authorizing its own recipient
(so the three existing mint call sites needed no changes); the two
buyer-facing purchase paths (`BuildCryptoPurchase`/`BuildFiatPurchase`)
now call `authorizeHolder` on the buyer's wallet and `requireBuyerKYC`
(`buyer.KYCVerifiedLevel == 0` -> `apperrors.Forbidden`, matching the
original's own `walletOwner.KYCVerified == 0` check) before building
the purchase - closing a real gap where neither path had ever checked
KYC. Full detail in `wallet-backend/PLAN.md` §22, including what's
deliberately not covered yet (secondary-market authorization for a
non-purchasing counterparty - flagged as real future work, not
silently skipped).

**Verified**: `flutter analyze` clean (the same 4 pre-existing
`info`-level suggestions as before this pass, no new warnings/errors);
all mobile tests pass, including 5 new widget tests for the pages
above and a `dashboard_drawer.png` golden regeneration for each of the
five new drawer entries added across this pass (Swap, Shared access,
Tokenize, Fund wallet, My Wallets).
