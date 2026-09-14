# Deploying wallet-web

A static SPA served behind nginx, with a WASM module built from Rust
source as part of the image build. There is no server-side runtime -
once built, this is static files plus response headers. See `PLAN.md`
for the design (in particular §7's threat model, which several of the
steps below exist to satisfy) and `app/.env.example` for build-time
configuration.

## 1. Prerequisites

- **Docker**, for the provided multi-stage build (recommended - it's the
  only build path that's actually been exercised end-to-end, see this
  project's own `PLAN.md` §10 implementation notes on what was and
  wasn't verified live).
- Building without Docker needs **Rust with the `wasm32-unknown-unknown`
  target, `wasm-pack`, and Node 20+** - see `README.md`'s "Running
  locally" section.
- **A running `wallet-backend` instance** to point at - this app has no
  backend of its own. Deploy that first (see its own `DEPLOYMENT.md`).

## 2. Build-time vs. runtime configuration - the one thing to get right

This is a static SPA: `VITE_WALLET_BACKEND_URL_TESTNET`/`_MAINNET` and
`VITE_CHAIN_ID_TESTNET`/`_MAINNET` (see `app/.env.example`) are **baked
into the JS bundle at build time**, not read from the environment at
container start. If you need to point the same built image at different
backends per deployment (dev/staging/prod), you must **rebuild** with
different `--build-arg` values for each - a
`docker run -e VITE_WALLET_BACKEND_URL_TESTNET=...` on the finished image
does nothing, since Vite has already inlined the previous value into the
bundle before that container ever starts.

**Both networks' config ships in every build.** Since PLAN.md's
testnet/mainnet switch, the app has an in-page Settings toggle
(`src/config/network.ts`, `src/pages/settings/Settings.tsx`) that picks
between them at runtime via `localStorage`, with no rebuild - so a single
build can offer both, and `VITE_DEFAULT_NETWORK` only controls which one
a fresh visit starts on. If a deployment should only ever offer one
network, leave the other pair blank; the switch disables (greys out) the
network whose backend URL is empty rather than letting someone select a
network that was never configured.

The **one exception** to "build-time only" is the CSP header's
`connect-src`, set from `BACKEND_URL_TESTNET`/`BACKEND_URL_MAINNET` via
nginx's `envsubst` templating (`app/nginx.conf.template`) at container
*start*, not build time - see step 4. Don't confuse the two:
`VITE_WALLET_BACKEND_URL_*` (build-time, in the JS) is what the app
actually calls; `BACKEND_URL_*` (start-time, in nginx) is only what the
browser's CSP is allowed to let it call. **Each pair must name the same
URL** (testnet build value = testnet nginx value, same for mainnet), or
the app will try to call an origin its own CSP then blocks.

## 3. Building

```bash
# from the wallet-web/ directory (the build context spans both
# wallet-core/ and app/, so it must not be run from inside app/)

# A build that offers both networks (e.g. an internal/staging build):
docker build -t wallet-web \
  --build-arg VITE_WALLET_BACKEND_URL_TESTNET=https://staging-api.your-domain.example \
  --build-arg VITE_CHAIN_ID_TESTNET=84532 \
  --build-arg VITE_WALLET_BACKEND_URL_MAINNET=https://api.your-domain.example \
  --build-arg VITE_CHAIN_ID_MAINNET=8453 \
  --build-arg VITE_DEFAULT_NETWORK=testnet \
  .

# A production build that should only ever offer mainnet (recommended
# for the actual public-facing deployment - see the pre-launch checklist,
# step 9): leave the testnet pair pointed wherever's convenient (or at
# the same values as dev) and set the default to mainnet, or simply never
# surface a way to reach the testnet build in your public DNS/routing.
docker build -t wallet-web \
  --build-arg VITE_WALLET_BACKEND_URL_MAINNET=https://api.your-domain.example \
  --build-arg VITE_CHAIN_ID_MAINNET=8453 \
  --build-arg VITE_DEFAULT_NETWORK=mainnet \
  .
```

`Dockerfile`'s app-builder stage declares `ARG`/`ENV` for all five
values (`VITE_WALLET_BACKEND_URL_TESTNET`, `VITE_CHAIN_ID_TESTNET`,
`VITE_WALLET_BACKEND_URL_MAINNET`, `VITE_CHAIN_ID_MAINNET`,
`VITE_DEFAULT_NETWORK`) and promotes each to a real process env var
before `npm run build:app-only` runs - Vite's own `loadEnv` merges
`process.env` over `app/.env.example`'s committed defaults, so no
generated `.env` file is needed; the `--build-arg`s above are sufficient
on their own.

## 4. Running - the nginx image and its templated CSP

```bash
docker run -p 8081:80 \
  -e BACKEND_URL_TESTNET=https://staging-api.your-domain.example \
  -e BACKEND_URL_MAINNET=https://api.your-domain.example \
  wallet-web
```

`app/nginx.conf.template` sets a `Content-Security-Policy` header whose
`connect-src` includes `${BACKEND_URL_TESTNET}` and
`${BACKEND_URL_MAINNET}`, substituted by the base nginx image's built-in
`docker-entrypoint.d` templating (`envsubst` over every `*.template` file
in `/etc/nginx/templates/`, per the `nginx:1.27-alpine` image's own
convention) at container start - both are allowlisted regardless of
which network is currently active in the browser, since the in-app
switch can call either one without a page reload from a fresh server.
Leaving one of these two unset renders as an empty `connect-src` entry,
which is harmless (nginx/the browser just ignore the blank token).
**Each one must be the exact same origin as its `VITE_WALLET_BACKEND_URL_*`
counterpart was at build time** (step 2's warning) - a mismatch here
doesn't fail to build or start, it fails silently at runtime: every API
call the app makes on that network gets blocked by its own CSP, and the
failure shows up only as "network request failed" in the browser, with a
CSP violation logged to the browser console that's easy to miss if
you're not looking for it.

## 5. TLS

The provided nginx config serves plain HTTP on port 80. Put this behind
a TLS-terminating reverse proxy or load balancer (or add a TLS server
block to `nginx.conf.template` yourself) before exposing it publicly -
serving a wallet application's UI over unencrypted HTTP is not an
acceptable production configuration regardless of how well the
client-side crypto is built, since an on-path attacker could otherwise
tamper with the page itself before any of this app's own protections
ever run.

## 6. CDN / caching

Static assets (`dist/assets/*`, including `wallet_core_bg.wasm`) are
content-hashed by Vite's build and safe to cache aggressively/serve from
a CDN. `index.html` should not be cached (or only briefly) so a new
deploy's hashed asset references actually reach clients promptly - the
provided nginx config doesn't set explicit cache-control headers; add
them (or configure your CDN/edge layer) if you put one in front of this.

## 7. Health check

There's no application health endpoint - use nginx's own liveness (a
plain `GET /` returning the SPA shell, HTTP 200) for your load
balancer's health check. A meaningful deeper check would be confirming
the served `index.html` actually references the current build's hashed
asset filenames, which is really just "did the last deploy succeed,"
not an ongoing runtime health concern for a static app.

## 8. What to verify before real traffic

This implementation's own `PLAN.md` §10 is explicit that the full
onboarding → send flow was **not** verified against a live
`wallet-backend` instance (none was available in the sandbox it was
built in) - only against a headless-browser run with the backend
unreachable, which exercised the wallet-creation/vault/WASM pipeline but
not an actual successful SIWE login, registration, or payment
broadcast. **Before routing real users at a production deployment**, run
through the full flow yourself end to end against your actual
`wallet-backend`: create a wallet, sign in, register, send a small
payment on Base Sepolia, and confirm it appears in
`wallet-payment-history-engine`'s indexed history.

## 9. Pre-launch checklist

- [ ] `VITE_WALLET_BACKEND_URL_TESTNET`/`_MAINNET` (build-time) and
      `BACKEND_URL_TESTNET`/`BACKEND_URL_MAINNET`
      (container-start-time, nginx CSP) each point at the exact same
      origin, pair by pair
- [ ] `VITE_CHAIN_ID_TESTNET`/`_MAINNET` matches the corresponding
      `wallet-backend`'s and `wallet-payment-history-engine`'s
      `BASE_CHAIN_ID` exactly
- [ ] For the public production deployment, decide deliberately whether
      it should offer the in-app testnet/mainnet switch at all - most
      deployments should set `VITE_DEFAULT_NETWORK=mainnet` and leave the
      testnet pair pointed at a real (non-empty) testnet backend only if
      you actually want end users able to reach it from production; if
      not, build a mainnet-only image instead (step 3's second example)
- [ ] Served over TLS, not plain HTTP
- [ ] The full onboarding → send flow run once, manually, end to end
      against the real `wallet-backend` deployment (step 8) - not just
      the build/type-check/unit-test pipeline
- [ ] `wallet-backend`'s CORS allowlist (`internal/middleware/cors.go`,
      see its own `DEPLOYMENT.md` §6) has been tightened from its
      wildcard-`*` default to actually include this app's real deployed
      origin - it works against the wildcard default with zero
      configuration, but that default should not reach production on
      either side
- [ ] Browser console checked for CSP violations after the first real
      deploy, not just a successful page load
