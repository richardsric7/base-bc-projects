import { getNonce, verifySiwe, type VerifyResult } from './authApi';
import { buildSiweMessage } from './siwe';
import { signSiweMessage } from '../core/walletCoreClient';
import type { WalletRole } from '../worker/protocol';
import { getNetworkConfig } from '../config/network';

/**
 * The full SIWE round trip (PLAN.md §2): fetch a nonce, build the
 * message, sign it with `role`'s key inside the Worker, and exchange the
 * signed message for a session JWT. `role` is normally "signer" - the
 * identity that authenticates to wallet-backend.
 */
export async function signInWithSiwe(role: WalletRole, address: string): Promise<VerifyResult> {
  const nonce = await getNonce();
  const message = buildSiweMessage({
    domain: window.location.host,
    address,
    statement: 'Sign in to Trovo Wallet.',
    uri: window.location.origin,
    chainId: getNetworkConfig().chainId,
    nonce,
  });
  const signature = await signSiweMessage(role, message);
  return verifySiwe(message, signature);
}
