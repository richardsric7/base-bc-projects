import { apiRequest } from './httpClient';
import { assertOnline } from '../connectivity/assertOnline';
import type { UnsignedTx } from './paymentsApi';

// wallet-backend's swaps component is a generic DEX router-call builder,
// not an abstracted "swap A for B" - the caller supplies the router
// contract, its ABI, and which method/args to call (PLAN.md §2:
// "the project configures which router it wants, this code doesn't pick
// for it"). wallet-web's own swap page (PLAN.md §9 Phase 11) is
// responsible for knowing a specific router's ABI/method shape for
// whichever DEX it targets.
export interface BuildSwapInput {
  routerAddress: string;
  routerAbi: string;
  method: string;
  args?: unknown[];
  valueWei?: string;
  nonce?: number;
}

export async function buildSwap(walletAddress: string, input: BuildSwapInput): Promise<UnsignedTx> {
  await assertOnline();
  return apiRequest<UnsignedTx>('/v1/swaps/build', { method: 'POST', walletAddress, body: input });
}

export function submitSwap(walletAddress: string, signedTx: string): Promise<{ hash: string }> {
  return apiRequest<{ hash: string }>('/v1/swaps/submit', { method: 'POST', walletAddress, body: { signedTx } });
}
