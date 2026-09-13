// A thin fetch wrapper for wallet-backend's REST API (PLAN.md §2). Every
// call here is a plain JSON request/response - the non-custodial parts
// (signing) never happen in this file, only in core/walletCoreClient.ts.
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

export const BASE_URL = import.meta.env.VITE_WALLET_BACKEND_URL ?? 'http://localhost:8080';

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
    response = await fetch(`${BASE_URL}${path}`, {
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
