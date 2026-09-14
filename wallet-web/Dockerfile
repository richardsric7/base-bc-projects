# syntax=docker/dockerfile:1.7

# Stage 1: build the wallet-core WASM module (needs the Rust toolchain,
# which the final Node/nginx stages don't) - PLAN.md §4.
FROM rust:1.82-slim AS wasm-builder
RUN rustup target add wasm32-unknown-unknown \
    && cargo install wasm-pack
WORKDIR /build
COPY wallet-core ./wallet-core
RUN wasm-pack build wallet-core --release --target web --out-dir /wasm-pkg

# Stage 2: build the React/Vite app against the already-built wasm-pkg.
# The four VITE_* build args become real process env vars before `vite
# build` runs - Vite's own `loadEnv` merges process.env over anything in
# a committed .env file, so these take effect with no .env file needed
# (see DEPLOYMENT.md §3). VITE_WALLET_BACKEND_URL_MAINNET defaults empty,
# same as app/.env.example, so a build that doesn't set it simply ships
# with the in-app network switch's "Mainnet" option disabled rather than
# pointed at nothing.
FROM node:20-alpine AS app-builder
ARG VITE_WALLET_BACKEND_URL_TESTNET=http://localhost:8080
ARG VITE_CHAIN_ID_TESTNET=84532
ARG VITE_WALLET_BACKEND_URL_MAINNET=
ARG VITE_CHAIN_ID_MAINNET=8453
ARG VITE_DEFAULT_NETWORK=testnet
ENV VITE_WALLET_BACKEND_URL_TESTNET=$VITE_WALLET_BACKEND_URL_TESTNET \
    VITE_CHAIN_ID_TESTNET=$VITE_CHAIN_ID_TESTNET \
    VITE_WALLET_BACKEND_URL_MAINNET=$VITE_WALLET_BACKEND_URL_MAINNET \
    VITE_CHAIN_ID_MAINNET=$VITE_CHAIN_ID_MAINNET \
    VITE_DEFAULT_NETWORK=$VITE_DEFAULT_NETWORK
WORKDIR /app
COPY app/package.json app/package-lock.json* ./
RUN npm ci
COPY app .
COPY --from=wasm-builder /wasm-pkg ./src/worker/wasm-pkg
RUN npm run build:app-only

# Stage 3: serve the static build behind nginx, with the CSP's connect-src
# templated to the real wallet-backend origin at container start.
FROM nginx:1.27-alpine
RUN apk add --no-cache gettext
COPY --from=app-builder /app/dist /usr/share/nginx/html
COPY app/nginx.conf.template /etc/nginx/templates/default.conf.template
EXPOSE 80
