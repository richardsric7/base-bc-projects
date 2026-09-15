// A separate, non-secret IndexedDB store from the encrypted vault
// (PLAN.md §6.4) - last-fetched curated tokens/balances/payment history,
// each stamped with when it was fetched, so the dashboard can render
// something meaningful offline with an honest "as of ..." timestamp
// instead of either nothing or a live-looking number. Runs on the main
// thread (unlike the vault, this data has no confidentiality
// requirement, so it doesn't need the Worker's isolation).

const DB_NAME = 'wallet-web-cache';
const DB_VERSION = 1;
const STORE_NAME = 'entries';

export interface CacheEntry<T> {
  key: string;
  data: T;
  fetchedAt: number;
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'key' });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export async function setCacheEntry<T>(key: string, data: T): Promise<void> {
  const db = await openDb();
  const entry: CacheEntry<T> = { key, data, fetchedAt: Date.now() };
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).put(entry);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function getCacheEntry<T>(key: string): Promise<CacheEntry<T> | undefined> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly');
    const request = tx.objectStore(STORE_NAME).get(key);
    request.onsuccess = () => resolve(request.result as CacheEntry<T> | undefined);
    request.onerror = () => reject(request.error);
  });
}

// Cache key builders - scoped per address so switching wallets never
// briefly shows the previous wallet's cached numbers (PLAN.md §6.4).
export const cacheKeys = {
  curatedTokens: () => 'curatedTokens',
  balances: (address: string) => `balances:${address}`,
  paymentHistory: (address: string) => `paymentHistory:${address}`,
  // The primary wallet's Safe address (PLAN.md §13/§17) is not itself
  // sensitive (it's public on-chain), and there is no local key to
  // "unlock" for it - caching it here (rather than treating it as a
  // vault) is what keeps offline reloads working without needing a live
  // GET /v1/users/:username round trip on every app start.
  primaryWalletAddress: (signerAddress: string) => `primaryWalletAddress:${signerAddress}`,
};
