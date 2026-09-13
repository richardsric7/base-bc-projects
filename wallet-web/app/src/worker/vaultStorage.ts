// A minimal, dependency-free IndexedDB wrapper for VaultRecord blobs
// (PLAN.md §5.1). Used only from inside the wallet-core Worker - the
// main thread never touches this database directly (PLAN.md §4.1).
// Deliberately hand-rolled rather than pulling in an IndexedDB helper
// library: the surface needed (put/get/delete by a single "role" key) is
// small enough that a dependency buys nothing but more supply-chain
// surface for a security-sensitive storage path.

const DB_NAME = 'wallet-web-vault';
const DB_VERSION = 1;
const STORE_NAME = 'vaults';

export interface StoredVaultRecord {
  role: 'signer' | 'primary';
  address: string;
  kdf: string;
  kdfParams: { memoryKiB: number; iterations: number; parallelism: number };
  salt: string;
  nonce: string;
  ciphertext: string;
  createdAt: number;
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'role' });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export async function putVaultRecord(record: StoredVaultRecord): Promise<void> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).put(record);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function getVaultRecord(role: 'signer' | 'primary'): Promise<StoredVaultRecord | undefined> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly');
    const request = tx.objectStore(STORE_NAME).get(role);
    request.onsuccess = () => resolve(request.result as StoredVaultRecord | undefined);
    request.onerror = () => reject(request.error);
  });
}

export async function deleteVaultRecord(role: 'signer' | 'primary'): Promise<void> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).delete(role);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}
