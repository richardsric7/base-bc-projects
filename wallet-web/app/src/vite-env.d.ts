/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_WALLET_BACKEND_URL_TESTNET: string;
  readonly VITE_WALLET_BACKEND_URL_MAINNET: string;
  readonly VITE_CHAIN_ID_TESTNET: string;
  readonly VITE_CHAIN_ID_MAINNET: string;
  readonly VITE_DEFAULT_NETWORK: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
