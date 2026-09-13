import { apiRequest } from './httpClient';

// wallet-backend's SIWE endpoints (PLAN.md §2: GET /v1/auth/nonce,
// POST /v1/auth/verify).
export function getNonce(): Promise<string> {
  return apiRequest<{ nonce: string }>('/v1/auth/nonce').then((r) => r.nonce);
}

export interface VerifyResult {
  address: string;
  token: string;
}

export function verifySiwe(message: string, signature: string): Promise<VerifyResult> {
  return apiRequest<VerifyResult>('/v1/auth/verify', { method: 'POST', body: { message, signature } });
}
