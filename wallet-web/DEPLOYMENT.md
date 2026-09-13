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

This is a static SPA: `VITE_WALLET_BACKEND_URL` and `VITE_CHAIN_ID` (see
`app/.env.example`) are **baked into the JS bundle at build time**, not
read from the environment at container start. If you need to point the
same built image at different backends per environment (dev/staging/
prod), you must **rebuild** with different `.env` values for each - a
`docker run -e VITE_WALLET_BACKEND_URL=...` on the finished image does
nothing, since Vite has already inlined the previous value into the
bundle before that container ever starts.

The **one exception** is the CSP header's `connect-src`, set from
`BACKEND_URL` via nginx's `envsubst` templating (`app/nginx.conf.template`)
at container *start*, not build time - see step 4. Don't confuse the two:
`VITE_WALLET_BACKEND_URL` (build-time, in the JS) is what the app
actually calls; `BACKEND_URL` (start-time, in nginx) is only what the
browser's CSP is allowed to let it call. **They must be the same URL**,
or the app will try to call an origin its own CSP then blocks.

## 3. Building

```bash
# from the wallet-web/ directory (the build context spans both
# wallet-core/ and app/, so it must not be run from inside app/)
docker build -t wallet-web \
  --build-arg VITE_WALLET_BACKEND_URL=https://api.your-domain.example \
  --build-arg VITE_CHAIN_ID=8453 \
  .
```

The current `Dockerfile` doesn't yet declare `ARG`/`ENV` for these two
build-time values - it builds `app/.env` as committed
(`app/.env.example`'s defaults, pointing at `localhost:8080`/Sepolia).
**Before a real deployment**, either add `ARG VITE_WALLET_BACKEND_URL`/
`ARG VITE_CHAIN_ID` to the app-builder stage and reference them in a
generated `.env`, or simplest: copy `app/.env.example` to `app/.env` with
your real production values *before* running `docker build`, since Vite
reads `.env` at its own build step inside the image regardless of how
that file got there.

## 4. Running - the nginx image and its templated CSP

```bash
docker run -p 8081:80 -e BACKEND_URL=https://api.your-domain.example wallet-web
```

`app/nginx.conf.template` sets a `Content-Security-Policy` header whose
`connect-src` is `${BACKEND_URL}`, substituted by the base nginx image's
built-in `docker-entrypoint.d` templating (`envsubst` over every
`*.template` file in `/etc/nginx/templates/`, per the `nginx:1.27-alpine`
image's own convention) at container start. **Set `BACKEND_URL` to the
exact same origin as `VITE_WALLET_BACKEND_URL` was at build time** (step
2's warning) - a mismatch here doesn't fail to build or start, it fails
silently at runtime: every API call the app makes gets blocked by its
own CSP, and the failure shows up only as "network request failed" in
the browser, with a CSP violation logged to the browser console that's
easy to miss if you're not looking for it.

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

- [ ] `VITE_WALLET_BACKEND_URL` (build-time) and `BACKEND_URL`
      (container-start-time, nginx CSP) point at the exact same origin
- [ ] `VITE_CHAIN_ID` matches `wallet-backend`'s and
      `wallet-payment-history-engine`'s `BASE_CHAIN_ID` exactly
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
