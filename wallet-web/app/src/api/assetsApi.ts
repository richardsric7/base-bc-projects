import { apiRequest } from './httpClient';
import { assertOnline } from '../connectivity/assertOnline';
import type { UnsignedTx } from './paymentsApi';

export interface CuratedToken {
  id: number;
  symbol: string;
  contractAddress: string;
  decimals: number;
  name: string;
  imageUrl: string;
  isActive: boolean;
}

export function listCuratedTokens(): Promise<CuratedToken[]> {
  return apiRequest<CuratedToken[]>('/v1/assets');
}

/** tokenAddress omitted/empty = native ETH balance (wallet-backend's
 * GET /v1/assets/balance/:address?token=... - PLAN.md §2). */
export function getBalance(address: string, tokenAddress?: string): Promise<string> {
  const query = tokenAddress ? `?token=${encodeURIComponent(tokenAddress)}` : '';
  return apiRequest<{ balance: string }>(`/v1/assets/balance/${address}${query}`).then((r) => r.balance);
}

export async function buildApprove(
  token: string,
  tokenAddress: string,
  spender: string,
  amount: string,
): Promise<UnsignedTx> {
  await assertOnline();
  return apiRequest<UnsignedTx>('/v1/assets/approve/build', {
    method: 'POST',
    token,
    body: { tokenAddress, spender, amount },
  });
}

export function submitApprove(token: string, signedTx: string): Promise<{ hash: string }> {
  return apiRequest<{ hash: string }>('/v1/assets/approve/submit', { method: 'POST', token, body: { signedTx } });
}
