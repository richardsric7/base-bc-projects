// The narrow, typed postMessage RPC contract between the main thread
// (core/walletCoreClient.ts) and the wallet-core Worker
// (worker/walletCoreWorker.ts) - PLAN.md §4.1/§4.2/§6.2. This file is
// pure types, imported by both sides, and never imports the wasm module
// itself - the main thread must never get a path to wallet-core beyond
// this exact request/response shape.

export type WalletRole = 'signer' | 'primary';

export type WorkerRequest =
  | { type: 'GENERATE_MNEMONIC'; wordCount: 12 | 24 }
  | { type: 'VALIDATE_MNEMONIC'; phrase: string }
  | { type: 'PREVIEW_ADDRESS'; phrase: string }
  | { type: 'CREATE_VAULT'; role: WalletRole; phrase: string; password: string }
  | { type: 'UNLOCK'; role: WalletRole; password: string }
  | { type: 'LOCK'; role: WalletRole }
  | { type: 'LOCK_ALL' }
  | { type: 'IS_UNLOCKED'; role: WalletRole }
  | { type: 'HAS_VAULT'; role: WalletRole }
  | { type: 'GET_KNOWN_ADDRESS'; role: WalletRole }
  | { type: 'SIGN_MESSAGE'; role: WalletRole; message: string }
  | { type: 'SIGN_HEX_DIGEST'; role: WalletRole; digestHex: string }
  | { type: 'SIGN_TRANSACTION'; role: WalletRole; unsignedTxJson: string }
  | { type: 'SIGN_LINK_PRIMARY'; message: string }
  | { type: 'WIPE'; role: WalletRole };

export interface WorkerResponsePayloads {
  GENERATE_MNEMONIC: { mnemonic: string };
  VALIDATE_MNEMONIC: { valid: boolean };
  PREVIEW_ADDRESS: { address: string };
  CREATE_VAULT: { address: string };
  UNLOCK: { address: string };
  LOCK: Record<string, never>;
  LOCK_ALL: Record<string, never>;
  IS_UNLOCKED: { unlocked: boolean };
  HAS_VAULT: { hasVault: boolean };
  GET_KNOWN_ADDRESS: { address: string | null };
  SIGN_MESSAGE: { signature: string };
  SIGN_HEX_DIGEST: { signature: string };
  SIGN_TRANSACTION: { signedTx: string };
  SIGN_LINK_PRIMARY: { signature: string };
  WIPE: Record<string, never>;
}

export type WorkerMessage = { id: string } & WorkerRequest;

export type WorkerReply =
  | { id: string; ok: true; result: WorkerResponsePayloads[keyof WorkerResponsePayloads] }
  | { id: string; ok: false; error: string };
