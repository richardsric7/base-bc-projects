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
FROM node:20-alpine AS app-builder
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
