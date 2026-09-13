/// <reference lib="webworker" />

// The Worker host for wallet-core (PLAN.md §4.1). This file, plus the
// wasm module it loads, is the only code in this app that ever handles a
// mnemonic or private key in cleartext. It never uses `postMessage` to
// send one back out - only addresses and signatures leave via `respond`.
//
// wasm-pack's `--target web` output is generated into ./wasm-pkg by
// `npm run build:wasm` (see package.json) - not committed, always built
// fresh from the wallet-core Rust source.
import init, * as walletCore from './wasm-pkg/wallet_core.js';
import type { WorkerMessage, WorkerReply, WalletRole } from './protocol';
import { putVaultRecord, getVaultRecord, deleteVaultRecord, type StoredVaultRecord } from './vaultStorage';

const workerScope = self as unknown as DedicatedWorkerGlobalScope;

const wasmReady = init();

// Failed-unlock exponential backoff (PLAN.md §5.3): slows scripted
// brute-forcing of a stolen/leaked encrypted vault from inside the page
// itself. Argon2id's own cost is the real defense here - this just
// removes the "script 10,000 guesses instantly" shortcut. Tracked per
// role, in this Worker's memory only, reset on a successful unlock.
const failedAttempts = new Map<WalletRole, number>();
const lastFailureAt = new Map<WalletRole, number>();

function backoffDelayMs(attempts: number): number {
  return Math.min(500 * 2 ** attempts, 30_000);
}

function remainingBackoffMs(role: WalletRole): number {
  const attempts = failedAttempts.get(role) ?? 0;
  if (attempts === 0) return 0;
  const elapsed = Date.now() - (lastFailureAt.get(role) ?? 0);
  return Math.max(0, backoffDelayMs(attempts) - elapsed);
}

async function handle(message: WorkerMessage): Promise<WorkerReply> {
  await wasmReady;
  try {
    switch (message.type) {
      case 'GENERATE_MNEMONIC': {
        const mnemonic = walletCore.generate_mnemonic(message.wordCount);
        return { id: message.id, ok: true, result: { mnemonic } };
      }
      case 'VALIDATE_MNEMONIC': {
        const valid = walletCore.validate_mnemonic(message.phrase);
        return { id: message.id, ok: true, result: { valid } };
      }
      case 'PREVIEW_ADDRESS': {
        const address = walletCore.derive_preview_address(message.phrase);
        return { id: message.id, ok: true, result: { address } };
      }
      case 'CREATE_VAULT': {
        const recordJson = walletCore.encrypt_vault(message.role, message.phrase, message.password, Date.now());
        const record = JSON.parse(recordJson) as StoredVaultRecord;
        await putVaultRecord(record);
        // Unlock immediately using the password already in hand, so a
        // freshly-created wallet is ready to sign right away (e.g. the
        // SIWE sign-in that follows signer creation) without asking for
        // the same password a second time in the same flow - not a new
        // capability, since creating the vault already required the
        // plaintext phrase.
        walletCore.unlock(message.role, recordJson, message.password);
        return { id: message.id, ok: true, result: { address: record.address } };
      }
      case 'UNLOCK': {
        const remaining = remainingBackoffMs(message.role);
        if (remaining > 0) {
          throw new Error(`Too many failed attempts. Try again in ${Math.ceil(remaining / 1000)}s.`);
        }
        const record = await getVaultRecord(message.role);
        if (!record) {
          throw new Error(`no wallet stored for role "${message.role}"`);
        }
        try {
          const resultJson = walletCore.unlock(message.role, JSON.stringify(record), message.password);
          const { address } = JSON.parse(resultJson) as { address: string };
          failedAttempts.delete(message.role);
          return { id: message.id, ok: true, result: { address } };
        } catch (err) {
          failedAttempts.set(message.role, (failedAttempts.get(message.role) ?? 0) + 1);
          lastFailureAt.set(message.role, Date.now());
          throw err;
        }
      }
      case 'LOCK': {
        walletCore.lock(message.role);
        return { id: message.id, ok: true, result: {} };
      }
      case 'LOCK_ALL': {
        walletCore.lock_all();
        return { id: message.id, ok: true, result: {} };
      }
      case 'IS_UNLOCKED': {
        const unlocked = walletCore.is_unlocked(message.role);
        return { id: message.id, ok: true, result: { unlocked } };
      }
      case 'HAS_VAULT': {
        const record = await getVaultRecord(message.role);
        return { id: message.id, ok: true, result: { hasVault: Boolean(record) } };
      }
      case 'GET_KNOWN_ADDRESS': {
        const record = await getVaultRecord(message.role);
        return { id: message.id, ok: true, result: { address: record?.address ?? null } };
      }
      case 'SIGN_SIWE': {
        const signature = walletCore.sign_siwe_message(message.role, message.message);
        return { id: message.id, ok: true, result: { signature } };
      }
      case 'SIGN_TRANSACTION': {
        const signedTx = walletCore.sign_transaction(message.role, message.unsignedTxJson);
        return { id: message.id, ok: true, result: { signedTx } };
      }
      case 'SIGN_LINK_PRIMARY': {
        const signature = walletCore.sign_link_primary_message(message.message);
        return { id: message.id, ok: true, result: { signature } };
      }
      case 'WIPE': {
        walletCore.lock(message.role);
        await deleteVaultRecord(message.role);
        return { id: message.id, ok: true, result: {} };
      }
      default: {
        const exhaustiveCheck: never = message;
        throw new Error(`unknown worker request: ${JSON.stringify(exhaustiveCheck)}`);
      }
    }
  } catch (err) {
    return { id: message.id, ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

workerScope.onmessage = (event: MessageEvent<WorkerMessage>) => {
  handle(event.data).then((reply) => workerScope.postMessage(reply));
};

// A locked-role check exists purely for the app's own idle-lock wiring
// convenience; role type re-export keeps a single source of truth for
// call sites that only need the role union, not the full protocol.
export type { WalletRole };
