// Client for wallet-backend's crypto component (PLAN.md §18/19):
// OneLiquidity-backed external-crypto deposit/withdrawal. Withdrawal is a
// real Safe transaction (the treasury debit) - build -> personal_sign
// digest -> confirm, same pattern as paymentsApi.ts, per wallet-backend
// PLAN.md §18's fix.
import { apiRequest } from './httpClient';

export interface DepositAddress {
  address: string;
  network: string;
}

export interface CryptoDeposit {
  currency: string;
  amount: string;
  txHash: string;
  createdAt: string;
}

export interface WithdrawalNetwork {
  network: string;
  withdrawMin: string;
  withdrawMax: string;
  withdrawFee: string;
}

export interface WithdrawalNetworksResult {
  networks: WithdrawalNetwork[];
  treasuryAddress: string;
}

export interface WithdrawalProposal {
  actionId: number;
  digestToSign: string;
}

export interface WithdrawalRequest {
  id: string;
  currency: string;
  network: string;
  toAddress: string;
  amountSubmitted: number;
  amountToWithdraw: number;
  status: string;
  treasuryTxHash: string;
  createdAt: string;
}

export function getDepositAddresses(walletAddress: string, currency: string): Promise<DepositAddress[]> {
  return apiRequest<DepositAddress[]>(`/v1/crypto/deposit-address/${encodeURIComponent(currency)}`, { walletAddress });
}

export function getDepositHistory(walletAddress: string): Promise<CryptoDeposit[]> {
  return apiRequest<CryptoDeposit[]>('/v1/crypto/deposit-history', { walletAddress });
}

export function getWithdrawalNetworks(walletAddress: string, currency: string): Promise<WithdrawalNetworksResult> {
  return apiRequest<WithdrawalNetworksResult>(`/v1/crypto/withdrawal-networks/${encodeURIComponent(currency)}`, {
    walletAddress,
  });
}

export function buildWithdrawal(
  walletAddress: string,
  currency: string,
  network: string,
  amount: number,
): Promise<WithdrawalProposal> {
  return apiRequest<WithdrawalProposal>('/v1/crypto/withdrawals/build', {
    method: 'POST',
    walletAddress,
    body: { currency, network, amount },
  });
}

export function confirmWithdrawal(
  walletAddress: string,
  currency: string,
  network: string,
  toAddress: string,
  amount: number,
  actionId: number,
  signature: string,
): Promise<WithdrawalRequest> {
  return apiRequest<WithdrawalRequest>('/v1/crypto/withdrawals/confirm', {
    method: 'POST',
    walletAddress,
    body: { currency, network, toAddress, amount, actionId, signature },
  });
}

export function getWithdrawalHistory(walletAddress: string): Promise<WithdrawalRequest[]> {
  return apiRequest<WithdrawalRequest[]>('/v1/crypto/withdrawal-history', { walletAddress });
}
