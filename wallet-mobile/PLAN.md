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
`sign_siwe_message`/`sign_transaction` (`signing.rs`). This is real,
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
| `sign_siwe_message` | Reused for the new per-request signature scheme too, despite the name - `wallet-backend/PLAN.md` §12.6 already flags this function as message-agnostic EIP-191 `personal_sign`; consider the rename to `sign_request_message` mentioned there happening once, shared by both apps, not twice |
| `sign_transaction` (EIP-1559) | Narrower role than originally scoped - kept for any plain-EOA signing need, but no longer how primary-wallet or sub-wallet payments/swaps are authorized (see below) |
| **New: EIP-712 typed-data signing** (`sign_typed_data`) | Needed for **every** payment or swap this app submits, not just shared-access approvals - once the primary wallet is a Safe (`wallet-backend/PLAN.md` §13.3, extended from sub-wallets-only by §15's recovery design), authorizing any transaction sourced from it means signing that transaction's `SafeTxHash` (EIP-712), not a plain `personal_sign`/raw EIP-1559 signature. This is a `wallet-core` gap on **both** platforms today (`wallet-web` doesn't have it either) - tracked here and in `wallet-web/PLAN.md` §12 as one shared addition to `signing.rs`, not a mobile-only one. |

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
approving a pending action signs a `SafeTxHash` via the new EIP-712
signer (§4.2), not an XDR co-signature via `signBase64Txn`; balance/asset
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
| `TrovoWalletSDK.signBase64Txn` | EIP-1559 tx signing / EIP-712 typed-data signing (§4.2) | `wallet-core` (FFI) |
| Sembast plaintext key storage | encrypted vault + OS Keychain/Keystore (§4.3) | `wallet-core` + `flutter_secure_storage` |
| Sub-wallet two-phase create+co-sign | single signed create request (§6) | app screen + `wallet-backend` |
| Shared-access grant/approve screens | same screens, EIP-712 approval signing | app screen + `wallet-core` |
| `processDeepLink` action switch | same switch, `+event` case, new QR source | app (`storage/`) |
| Theme (`custom_bloc_observer/`) | ported palette/typography, same token names where practical | app (`theme/`) |
| `screens/account_recovery/` | wallet-signer recovery (§9) | app screen + `wallet-core` + `wallet-backend` §15 |

## 11. Cross-project dependencies (tracked here, resolved elsewhere)

- Needs `wallet-backend/PLAN.md` §12 (per-request signature auth), §13
  (Safe-based sub-wallets/shared-access, now covering the primary wallet
  too), §14 (servicelinks QR), and §15 (wallet recovery) implemented and
  stable before this app's networking/sub-wallet/shared-access/
  servicelinks/recovery layers can be built against real endpoints.
- Needs `wallet-core` to grow: FFI bindings (§4.1), EIP-712 typed-data
  signing (§4.2 - shared with `wallet-web`, tracked in that project's own
  `PLAN.md` too so it isn't built twice), and the request-signing
  function rename if `wallet-backend`/`wallet-web` settle on one.

## 12. Open decisions

- `flutter_rust_bridge` vs `uniffi` for the FFI layer (§4.1 recommends
  the former).
- Whether to drop the unused `flutter_bloc`/`get` dependencies entirely
  and standardize on `provider` alone (recommended - they carry no real
  state in the original either), or keep them for parity with the
  original's dependency list.
- `APPROVER` vs `AUTHORIZER` naming, shared with `wallet-backend/
  PLAN.md` §13.8/§13.9.

## 13. Phased build roadmap (not started - design only)

| Phase | Scope | Depends on |
|---|---|---|
| 1 | `wallet-core`: FFI bindings (§4.1), EIP-712 signing addition (§4.2) | `wallet-web/PLAN.md`'s tracked EIP-712 item |
| 2 | App scaffold: Flutter project, ported theme (§3, colors/fonts/`ColorNotifier`), router port | Phase 1 |
| 3 | Vault + secure storage (§4.3), onboarding/import screens | Phase 1, 2 |
| 4 | Networking layer with the finalized signature-auth headers (§5) and the testnet/mainnet environment switch (§5.1) | `wallet-backend` §12 shipped |
| 5 | Dashboard, send/receive, swap - straightforward ports | Phase 4 |
| 6 | Sub-wallets (§6), shared access (§7) | `wallet-backend` §13 shipped |
| 7 | Servicelinks QR scanning + approval screens, including the new `EVENT` case (§8) | `wallet-backend` §14 shipped |
| 8 | Wallet recovery enable/disable + execution screens (§9) | `wallet-backend` §15 shipped |
| 9 | KYC, subscriptions, asset tokenization, backup/delete-account | Phase 5 |
| 10 | Full integration pass against Base Sepolia; push | Everything above |

Implementation does not begin until explicitly authorized.
