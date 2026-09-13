import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The wasm-core Worker (src/worker/walletCoreWorker.ts) is loaded with
// `new Worker(new URL(..., import.meta.url), { type: 'module' })` - Vite
// handles bundling both the worker and the wasm-pack `--target web`
// output it imports (a plain ESM module + a `new URL(...)`-referenced
// .wasm asset) with no extra plugin required.
export default defineConfig({
  plugins: [react()],
  build: {
    target: 'esnext', // top-level await in the generated wasm-bindgen glue
  },
  worker: {
    format: 'es',
  },
});
