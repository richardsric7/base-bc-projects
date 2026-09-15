// Client for wallet-backend's stablerail component (PLAN.md §18/19):
// Stablerail-backed NGN bank onboarding and onramp (cNGN). Every route
// requires the wallet's own signed request.
import { apiRequest } from './httpClient';

export interface Bank {
  code: string;
  name: string;
}

export function listBanks(walletAddress: string): Promise<Bank[]> {
  return apiRequest<Bank[]>('/v1/stablerail/banks', { walletAddress });
}

export function initiateOnboarding(walletAddress: string, bvn: string): Promise<{ message: string }> {
  return apiRequest<{ message: string }>(`/v1/stablerail/onboard/${encodeURIComponent(bvn)}`, {
    method: 'POST',
    walletAddress,
  });
}

export function initiateOnramp(walletAddress: string, amount: number): Promise<unknown> {
  return apiRequest(`/v1/stablerail/onramp/${amount}`, { method: 'POST', walletAddress });
}
