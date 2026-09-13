# wallet-web

A non-custodial web wallet for `wallet-backend` (the Base port of the
Trovo wallet API) and `wallet-payment-history-engine`. See
[`PLAN.md`](./PLAN.md) for the full design: theme, security architecture,
the signer/primary-wallet model, and the offline/online design.

## Structure

```
wallet-web/
├── PLAN.md
├── Dockerfile              # multi-stage: wasm-core -> app build -> nginx
├── wallet-core/            # Rust crate -> WASM (PLAN.md §4) - the only
│   │                       # place a mnemonic/private key exists in cleartext
│   └── src/
│       ├── mnemonic.rs     # BIP-39 + hand-rolled BIP-32/44 HD derivation
│       ├── vault.rs        # Argon2id + AES-256-GCM encrypted vault
│       ├── signing.rs      # EIP-191 personal_sign (SIWE) + EIP-1559 tx signing
│       ├── rlp.rs          # minimal RLP encoder for EIP-1559 transactions
│       └── lib.rs          # the entire wasm-bindgen surface (PLAN.md §4.2)
└── app/                    # Vite + React 18 + TypeScript SPA
    └── src/
        ├── worker/         # the Worker host running wallet-core (PLAN.md §4.1)
        ├── core/           # walletCoreClient.ts - the only way the main thread reaches the Worker
        ├── connectivity/   # online/offline detection + indicator (PLAN.md §6.4)
        ├── cache/          # non-secret offline read cache (IndexedDB)
        ├── api/            # wallet-backend REST client
        ├── security/       # idle-lock, cross-tab lock sync, clipboard auto-clear
        ├── store/          # Redux Toolkit - UI/app state only, never key material
        ├── components/, layout/, pages/
        └── App.tsx
```

## Running locally

```bash
cd wallet-web/app
cp .env.example .env   # point at your wallet-backend instance
npm install
npm run dev            # runs build:wasm first, then starts Vite
```

Requires the Rust toolchain with the `wasm32-unknown-unknown` target and
`wasm-pack` installed to build `wallet-core` (`npm run build:wasm` does
this automatically as part of `dev`/`build`).

## Testing

```bash
# wallet-core (Rust)
cd wallet-core
cargo test
cargo build --target wasm32-unknown-unknown

# app (TypeScript)
cd app
npx tsc --noEmit
npm run build
```

## Deploying

`Dockerfile` (at the `wallet-web/` root, since its build context spans
both `wallet-core/` and `app/`) builds the WASM module, then the app,
then serves the static output behind nginx with the CSP header from
`app/nginx.conf.template` (its `connect-src` is templated to your real
`BACKEND_URL` at container start, matching `trovo-wallet-monorepo/web`'s
own nginx pattern).

```bash
docker build -t wallet-web -f Dockerfile .
docker run -p 8081:80 -e BACKEND_URL=https://your-wallet-backend.example.com wallet-web
```

## Known, tracked, non-blocking issues

- `react-router-dom` and `vite`'s transitive `esbuild` both have open
  moderate/high-severity advisories (`npm audit`) whose fixes are major
  version bumps. Neither is meaningfully exploitable in this app's actual
  usage today (no SSR; no user-controlled navigation targets reach
  `<Link>`/`useNavigate`; `esbuild`'s dev-server-only issue doesn't affect
  the production build), but both are worth planned upgrades rather than
  permanently ignored findings.
- The onboarding wizard's step state is not persisted - if a user
  abandons the flow after creating a signer wallet but before completing
  primary-wallet setup, reloading resumes at step 1 rather than exactly
  where they left off (the signer vault itself is preserved; starting
  over would create a fresh one and orphan the first). Not a security
  issue, a UX polish item for a later pass.
