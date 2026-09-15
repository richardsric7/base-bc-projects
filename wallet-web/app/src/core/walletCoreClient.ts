// The main thread's only way to reach wallet-core (PLAN.md §4.1/§6.1):
// spawns the Worker once, correlates requests/responses by id, and
// exposes a small typed async function per operation. No other file in
// the app's main-thread code should construct a Worker or postMessage to
// it directly - go through these functions.
import type { WalletRole, WorkerRequest, WorkerReply, WorkerResponsePayloads } from '../worker/protocol';
import { broadcastLock } from '../security/lockSync';

let worker: Worker | null = null;
let nextId = 0;
const pending = new Map<string, { resolve: (value: unknown) => void; reject: (reason: Error) => void }>();

function getWorker(): Worker {
  if (!worker) {
    worker = new Worker(new URL('../worker/walletCoreWorker.ts', import.meta.url), { type: 'module' });
    worker.onmessage = (event: MessageEvent<WorkerReply>) => {
      const reply = event.data;
      const waiting = pending.get(reply.id);
      if (!waiting) return;
      pending.delete(reply.id);
      if (reply.ok) {
        waiting.resolve(reply.result);
      } else {
        waiting.reject(new Error(reply.error));
      }
    };
  }
  return worker;
}

function call<T extends WorkerRequest['type']>(
  message: Extract<WorkerRequest, { type: T }>,
): Promise<WorkerResponsePayloads[T]> {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve: resolve as (value: unknown) => void, reject });
    getWorker().postMessage({ ...message, id });
  });
}

export function generateMnemonic(wordCount: 12 | 24): Promise<string> {
  return call({ type: 'GENERATE_MNEMONIC', wordCount }).then((r) => r.mnemonic);
}

export function validateMnemonic(phrase: string): Promise<boolean> {
  return call({ type: 'VALIDATE_MNEMONIC', phrase }).then((r) => r.valid);
}

export function previewAddress(phrase: string): Promise<string> {
  return call({ type: 'PREVIEW_ADDRESS', phrase }).then((r) => r.address);
}

export function createVault(role: WalletRole, phrase: string, password: string): Promise<string> {
  return call({ type: 'CREATE_VAULT', role, phrase, password }).then((r) => r.address);
}

export function unlockWallet(role: WalletRole, password: string): Promise<string> {
  return call({ type: 'UNLOCK', role, password }).then((r) => r.address);
}

export function lockWallet(role: WalletRole): Promise<void> {
  return call({ type: 'LOCK', role }).then(() => {
    broadcastLock(); // PLAN.md §5.3: propagate to every other open tab
  });
}

/** Locks this tab's Worker session only - no broadcast. Used both by
 * `lockAllWallets` (below) and by the handler that reacts to another
 * tab's broadcast, so receiving a lock message never re-broadcasts it
 * and ping-pongs between tabs. */
export function lockAllWalletsLocal(): Promise<void> {
  return call({ type: 'LOCK_ALL' }).then(() => undefined);
}

export function lockAllWallets(): Promise<void> {
  return lockAllWalletsLocal().then(() => {
    broadcastLock();
  });
}

export function isUnlocked(role: WalletRole): Promise<boolean> {
  return call({ type: 'IS_UNLOCKED', role }).then((r) => r.unlocked);
}

export function hasVault(role: WalletRole): Promise<boolean> {
  return call({ type: 'HAS_VAULT', role }).then((r) => r.hasVault);
}

export function getKnownAddress(role: WalletRole): Promise<string | null> {
  return call({ type: 'GET_KNOWN_ADDRESS', role }).then((r) => r.address);
}

/** Signs an arbitrary message (EIP-191 personal_sign) with `role`'s key -
 * used for wallet-backend's per-request SignatureAuth headers (PLAN.md
 * §11: `fullPathWithQuery + signerAddress + timestamp`, always signed
 * with the "signer" role's key regardless of which wallet a request acts
 * on) and any future shared-access approval signature. */
export function signRequestMessage(role: WalletRole, message: string): Promise<string> {
  return call({ type: 'SIGN_MESSAGE', role, message }).then((r) => r.signature);
}

/** Signs a `0x`-prefixed hex digest (a Safe transaction hash returned as
 * `digestToSign`/`*SafeTxHash` by payments/swaps/asset-approve `/build`
 * and wallet-recovery's enable/disable challenges) - NOT
 * `signRequestMessage`, despite both being EIP-191 `personal_sign`: this
 * one hashes the digest's raw decoded bytes, matching what
 * wallet-backend's `cryptoutil.VerifyPersonalSignBytes` actually verifies
 * (see wallet-core's `signing::sign_hex_digest` doc comment for the real
 * bug this fixes - `signRequestMessage` would instead hash the ASCII hex
 * string and never verify). */
export function signHexDigest(role: WalletRole, digestHex: string): Promise<string> {
  return call({ type: 'SIGN_HEX_DIGEST', role, digestHex }).then((r) => r.signature);
}

export function signTransaction(role: WalletRole, unsignedTxJson: string): Promise<string> {
  return call({ type: 'SIGN_TRANSACTION', role, unsignedTxJson }).then((r) => r.signedTx);
}

export function signLinkPrimaryMessage(message: string): Promise<string> {
  return call({ type: 'SIGN_LINK_PRIMARY', message }).then((r) => r.signature);
}

export function wipeWallet(role: WalletRole): Promise<void> {
  return call({ type: 'WIPE', role }).then(() => {
    broadcastLock(); // a wallet removed in one tab must not stay usable in another
  });
}
