// A thin fetch wrapper for wallet-backend's REST API (PLAN.md §2). Signing
// itself still happens only in core/walletCoreClient.ts (via the Worker) -
// this file just knows *when* a request needs a signature and builds the
// exact message wallet-backend's SignatureAuth middleware expects
// (PLAN.md §11, internal/middleware/signature_auth.go): every
// authenticated request signs `fullPathWithQuery + signerAddress +
// timestamp` with the signer role's key and attaches the result as four
// headers - there is no session/login step or bearer token in this
// scheme, unlike the SIWE+JWT approach this replaced.
import { getNetworkConfig } from '../config/network';
import { getKnownAddress, signRequestMessage } from '../core/walletCoreClient';

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
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  body?: unknown;
  /**
   * The wallet address this request acts on (wallet-backend's
   * `X-Wallet-Address`) - required for every SignatureAuth-protected
   * route, omitted for public ones. The request is always signed with
   * the **signer** role's key (PLAN.md §11), regardless of which wallet
   * `walletAddress` names - e.g. a payment sent from the primary wallet
   * is still authorized by the signer's own signature, the same way a
   * shared-access member acts on a group wallet they don't themselves
   * own. For a registration request (no wallet exists yet to name),
   * pass the signer's own address - wallet-backend requires signer ===
   * wallet in that one case (see usersApi.ts's registerUser).
   */
  walletAddress?: string;
}

async function buildSignatureAuthHeaders(
  path: string,
  walletAddress: string,
): Promise<Record<string, string>> {
  const signerAddress = await getKnownAddress('signer');
  if (!signerAddress) {
    throw new Error('Signer wallet is locked or not set up - unlock it before making this request.');
  }
  const timestamp = Math.floor(Date.now() / 1000).toString();
  // Must match wallet-backend's own construction exactly:
  // c.Request.URL.RequestURI() + signer + timestampStr - i.e. `path`
  // here already includes any query string, with no method/host/scheme.
  const message = path + signerAddress + timestamp;
  const signature = await signRequestMessage('signer', message);
  return {
    'X-Signer-Address': signerAddress,
    'X-Wallet-Address': walletAddress,
    'X-Signature': signature,
    'X-Timestamp': timestamp,
  };
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (options.walletAddress) {
    Object.assign(headers, await buildSignatureAuthHeaders(path, options.walletAddress));
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
