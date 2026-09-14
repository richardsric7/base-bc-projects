// A thin fetch wrapper for wallet-backend's REST API (PLAN.md §2). Every
// call here is a plain JSON request/response - the non-custodial parts
// (signing) never happen in this file, only in core/walletCoreClient.ts.
import { getNetworkConfig } from '../config/network';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

// Read fresh on every call (not cached at module load) so the in-app
// testnet/mainnet switch (src/config/network.ts) takes effect without
// needing this module to be re-imported.
export function getBaseUrl(): string {
  return getNetworkConfig().backendUrl;
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'DELETE';
  body?: unknown;
  token?: string | null;
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }

  let response: Response;
  try {
    response = await fetch(`${getBaseUrl()}${path}`, {
      method: options.method ?? 'GET',
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    });
  } catch {
    // A network-level failure (offline, DNS, refused connection) - the
    // connectivity module (PLAN.md §6.4) is the authoritative signal for
    // "are we online", but any individual call can still fail this way
    // even when that status briefly said otherwise (a stale/manipulated
    // status can only ever cause a request that fails cleanly here, per
    // PLAN.md §7's threat-model note).
    throw new ApiError(0, 'Network request failed - check your connection');
  }

  const text = await response.text();
  const data = text ? JSON.parse(text) : undefined;

  if (!response.ok) {
    const message = (data && (data.message || data.error)) || `Request failed with status ${response.status}`;
    throw new ApiError(response.status, message);
  }

  return data as T;
}
