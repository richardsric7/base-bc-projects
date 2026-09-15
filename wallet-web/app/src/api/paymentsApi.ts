import { apiRequest } from './httpClient';
import { assertOnline } from '../connectivity/assertOnline';

// What wallet-backend's /build now returns (PLAN.md §17): every wallet is
// a Safe smart-contract account with no private key of its own, so there
// is no "unsigned transaction" for a client to sign directly anymore -
// /build proposes the transfer as a real Safe transaction and returns the
// digest the caller's own signer key must personal_sign to approve it
// (see core/walletCoreClient.ts's signRequestMessage). Shared with
// swapsApi.ts, which gets the identical shape from /v1/swaps/build.
export interface ActionProposal {
  actionId: number;
  digestToSign: string;
  // resolvedAddress is only present on a payment proposal (wallet-backend's
  // payments.PaymentProposal) - what `destination` actually resolved to,
  // since it may be an address, username, email, or wallet alias
  // (users.UserWallet's own doc comment). swapsApi.ts's identically-shaped
  // proposal has no recipient to resolve, so this is always absent there.
  resolvedAddress?: string;
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
): Promise<ActionProposal> {
  await assertOnline(); // PLAN.md §6.4: re-verified here, not just at the UI layer
  return apiRequest<ActionProposal>('/v1/payments/build', {
    method: 'POST',
    walletAddress,
    body: { destination, tokenAddress: tokenAddress ?? '', amount },
  });
}

export function submitPayment(
  walletAddress: string,
  idempotencyKey: string,
  actionId: number,
  signature: string,
  destination: string,
  amount: string,
  tokenAddress?: string,
): Promise<PaymentHistoryRecord> {
  return apiRequest<PaymentHistoryRecord>('/v1/payments/submit', {
    method: 'POST',
    walletAddress,
    body: { idempotencyKey, actionId, signature, destination, tokenAddress: tokenAddress ?? '', amount },
  });
}

// GET /v1/payments/history/:address is SignatureAuth-protected (unlike
// GET /v1/assets and GET /v1/assets/balance/:address, which are public) -
// internal/components/payments/controllers/controllers.go puts it in the
// same `authed` group as build/submit. `address` is both the history
// being requested and the wallet the signer must own/have standing on.
export function getPaymentHistory(address: string): Promise<PaymentHistoryRecord[]> {
  return apiRequest<PaymentHistoryRecord[]>(`/v1/payments/history/${address}`, { walletAddress: address });
}
