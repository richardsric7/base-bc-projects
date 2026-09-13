import { apiRequest, ApiError } from './httpClient';

export interface User {
  id: number;
  username: string;
  email: string;
  address: string;
  kycStatus: string;
}

// POST /v1/users (authed) - address comes from the verified SIWE session
// server-side, never from this body (PLAN.md §2).
export function registerUser(token: string, username: string, email: string): Promise<User> {
  return apiRequest<User>('/v1/users', { method: 'POST', token, body: { username, email } });
}

export function getUser(username: string): Promise<User> {
  return apiRequest<User>(`/v1/users/${encodeURIComponent(username)}`);
}

export class LinkPrimaryNotSupportedError extends Error {
  constructor() {
    super('This wallet-backend deployment does not yet support linking a separate primary wallet.');
    this.name = 'LinkPrimaryNotSupportedError';
  }
}

/**
 * PLAN.md §3's proposed `POST /v1/users/wallets/link-primary` - not yet
 * implemented by wallet-backend. Calls it anyway (a future deployment
 * may have it) and translates a 404 into a distinguishable error so the
 * UI can fall back to "same mnemonic for both" messaging instead of
 * showing a raw network error.
 */
export async function linkPrimaryWallet(
  token: string,
  address: string,
  message: string,
  signature: string,
): Promise<void> {
  try {
    await apiRequest<void>('/v1/users/wallets/link-primary', {
      method: 'POST',
      token,
      body: { address, message, signature },
    });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      throw new LinkPrimaryNotSupportedError();
    }
    throw err;
  }
}
