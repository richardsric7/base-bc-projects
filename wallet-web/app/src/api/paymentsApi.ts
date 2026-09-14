import { apiRequest } from './httpClient';
import { assertOnline } from '../connectivity/assertOnline';

// Mirrors wallet-backend's network.UnsignedTx exactly (PLAN.md §2,
// internal/network/base.go): chainId/value/data/maxFeePerGas/
// maxPriorityFeePerGas are 0x-prefixed hex strings, nonce/gas are plain
// JSON numbers - the same shape wallet-core's sign_transaction expects.
export interface UnsignedTx {
  chainId: string;
  nonce: number;
  to?: string;
  value: string;
  data: string;
  gas: number;
  maxFeePerGas: string;
  maxPriorityFeePerGas: string;
  type: string;
}

export interface PaymentHistoryRecord {
  idempotencyKey: string;
  fromAddress: string;
  toAddress: string;
  tokenAddress: string;
  amount: string;
  txHash: string;
  createdAt: string;
}

export async function buildPayment(
  walletAddress: string,
  destination: string,
  amount: string,
  tokenAddress?: string,
  nonce?: number,
): Promise<UnsignedTx> {
  await assertOnline(); // PLAN.md §6.4: re-verified here, not just at the UI layer
  return apiRequest<UnsignedTx>('/v1/payments/build', {
    method: 'POST',
    walletAddress,
    body: { destination, tokenAddress: tokenAddress ?? '', amount, nonce },
  });
}

export function submitPayment(
  walletAddress: string,
  idempotencyKey: string,
  signedTx: string,
  destination: string,
  amount: string,
  tokenAddress?: string,
): Promise<PaymentHistoryRecord> {
  return apiRequest<PaymentHistoryRecord>('/v1/payments/submit', {
    method: 'POST',
    walletAddress,
    body: { idempotencyKey, signedTx, destination, tokenAddress: tokenAddress ?? '', amount },
  });
}

export function getPaymentHistory(address: string): Promise<PaymentHistoryRecord[]> {
  return apiRequest<PaymentHistoryRecord[]>(`/v1/payments/history/${address}`);
}
