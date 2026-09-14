// Runtime testnet/mainnet switch. wallet-backend is deployed once per
// network (they're separate services with separate databases, not one
// service serving two chains - see wallet-backend/DEPLOYMENT.md), so
// "switching network" here really means "point this app at a different
// wallet-backend deployment" - the API base URL and chain ID both change
// together, never independently.
//
// VITE_WALLET_BACKEND_URL_TESTNET/_MAINNET and VITE_CHAIN_ID_TESTNET/
// _MAINNET (see .env.example) are build-time Vite values, baked into the
// bundle like any other VITE_* var - both networks' values ship in the
// same build, and the choice of which one is active is a runtime toggle
// stored in localStorage, not a rebuild. This lets one deployed app
// (e.g. a staging build) offer the in-app switch this file exists for;
// see PLAN.md and DEPLOYMENT.md's CSP note for why both origins must be
// allowlisted at build time either way.
export type NetworkEnv = 'mainnet' | 'testnet';

export interface NetworkConfig {
  env: NetworkEnv;
  label: string;
  backendUrl: string;
  chainId: number;
}

const STORAGE_KEY = 'trovo.activeNetwork';

function readEnv(name: string, fallback: string): string {
  return (import.meta.env as unknown as Record<string, string | undefined>)[name] ?? fallback;
}

const NETWORKS: Record<NetworkEnv, NetworkConfig> = {
  testnet: {
    env: 'testnet',
    label: 'Base Sepolia (Testnet)',
    backendUrl: readEnv('VITE_WALLET_BACKEND_URL_TESTNET', 'http://localhost:8080'),
    chainId: Number(readEnv('VITE_CHAIN_ID_TESTNET', '84532')),
  },
  mainnet: {
    env: 'mainnet',
    label: 'Base Mainnet',
    backendUrl: readEnv('VITE_WALLET_BACKEND_URL_MAINNET', ''),
    chainId: Number(readEnv('VITE_CHAIN_ID_MAINNET', '8453')),
  },
};

// A build that only ever targets one network (the common case - most
// deployments are one app build per environment, per DEPLOYMENT.md) can
// leave VITE_DEFAULT_NETWORK unset; it defaults to testnet for the same
// forgotten-env-var-safety reason wallet-backend's own BASE_CHAIN_ID
// defaults to Sepolia.
const DEFAULT_NETWORK: NetworkEnv = readEnv('VITE_DEFAULT_NETWORK', 'testnet') === 'mainnet' ? 'mainnet' : 'testnet';

export function getActiveNetwork(): NetworkEnv {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === 'mainnet' || stored === 'testnet') return stored;
  } catch {
    // localStorage unavailable (private browsing, etc.) - fall back to the build default below.
  }
  return DEFAULT_NETWORK;
}

export function getNetworkConfig(network: NetworkEnv = getActiveNetwork()): NetworkConfig {
  return NETWORKS[network];
}

export function isNetworkConfigured(network: NetworkEnv): boolean {
  return NETWORKS[network].backendUrl !== '';
}

/**
 * Persists the chosen network and reloads the app. A reload (rather than
 * an in-place state update) is deliberate: testnet and mainnet are
 * different wallet-backend deployments with entirely separate user
 * registrations, wallet addresses, and balances - there is no
 * "translating" in-memory Redux/cache state from one to the other, so
 * the safest thing is to let the app re-initialize from scratch exactly
 * as it would on a fresh visit, now pointed at the new backend.
 */
export function switchActiveNetwork(network: NetworkEnv): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, network);
  } catch {
    // If we can't persist it, the reload below would just come back to
    // the build default - nothing further we can do without storage.
  }
  window.location.reload();
}
