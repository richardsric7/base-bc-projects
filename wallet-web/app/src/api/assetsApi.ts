import { apiRequest } from './httpClient';
import { assertOnline } from '../connectivity/assertOnline';
import type { ActionProposal } from './paymentsApi';

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
  walletAddress: string,
  tokenAddress: string,
  spender: string,
  amount: string,
): Promise<ActionProposal> {
  await assertOnline();
  return apiRequest<ActionProposal>('/v1/assets/approve/build', {
    method: 'POST',
    walletAddress,
    body: { tokenAddress, spender, amount },
  });
}

export function submitApprove(walletAddress: string, actionId: number, signature: string): Promise<{ hash: string }> {
  return apiRequest<{ hash: string }>('/v1/assets/approve/submit', {
    method: 'POST',
    walletAddress,
    body: { actionId, signature },
  });
}
