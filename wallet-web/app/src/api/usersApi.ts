import { apiRequest, ApiError } from './httpClient';

export interface User {
  id: number;
  username: string;
  email: string;
  // The primary wallet's Safe smart-contract address (PLAN.md §13),
  // computed and stored by wallet-backend at registration time - never a
  // separately-imported mnemonic's own address (PLAN.md §3, corrected -
  // a Safe has no private key of its own).
  address: string;
  signerAddress: string;
  primaryWalletDeployed: boolean;
  kycStatus: string;
  // See PLAN.md §13's own recovery UI section: Branch A (free, DB-only
  // address swap) and Branch B (paid, true wallet-signer recovery via a
  // Safe owner swap) are independent toggles - a user may have neither,
  // either, or both enabled at once.
  accountRecoveryEnabled: boolean;
  walletRecoveryEnabled: boolean;
}

// POST /v1/users (SignatureAuth) - the registered address comes from the
// verified request signature server-side, never from this body (PLAN.md
// §2/§11/§12). No wallet exists yet to name in X-Wallet-Address, so this
// is necessarily self-signed: pass the signer's own address as
// signerAddress, used as both X-Signer-Address and X-Wallet-Address.
export function registerUser(signerAddress: string, username: string, email: string): Promise<User> {
  return apiRequest<User>('/v1/users', { method: 'POST', walletAddress: signerAddress, body: { username, email } });
}

// GET /v1/users/me (SignatureAuth) - looks a profile up by the verified
// signer's own address. Used by onboarding (to tell "this signer already
// has a registered profile" from "brand-new signer") and by App.tsx's
// reload hydration (to re-resolve the primary wallet address if the
// offline cache was cleared).
export function getMyUser(signerAddress: string): Promise<User> {
  return apiRequest<User>('/v1/users/me', { walletAddress: signerAddress });
}

// GET /v1/users/:username (public) - used by the recovery flow (no
// signer key survives to authenticate with at that point) to check which
// recovery branch(es) a username has enabled before asking for the
// matching factors. Also usable generally as a public username lookup.
export function getUserByUsername(username: string): Promise<User> {
  return apiRequest<User>(`/v1/users/${encodeURIComponent(username)}`);
}

// POST /v1/users/wallet/deploy (SignatureAuth, self-signed) - submits the
// on-chain deployment of the caller's own primary wallet Safe at the
// address Register already computed (wallet-backend PLAN.md §13/§17).
// Idempotent and paid for by a platform-operated key, so it's safe to call
// freely - in particular, right after onboarding, since a Safe that is
// never deployed has no shared-access group and can't build/submit any
// payment or swap yet (see PLAN.md §17.4).
export function deployPrimaryWallet(signerAddress: string): Promise<User> {
  return apiRequest<User>('/v1/users/wallet/deploy', { method: 'POST', walletAddress: signerAddress });
}

// A payment/swap/approve `build` call 404s with this specific message when
// the primary wallet's Safe hasn't been deployed yet (services.go's
// GroupWalletExecutor finds no ClosedGroup for it - see
// deployPrimaryWallet's doc comment above). Onboarding already attempts
// deployment once, best-effort; this is the backstop for whenever that
// attempt didn't stick (device was offline at the time, or a transient RPC
// failure) - it deploys and retries the same build call once more before
// giving up.
export async function withWalletDeployRetry<T>(signerAddress: string, buildCall: () => Promise<T>): Promise<T> {
  try {
    return await buildCall();
  } catch (err) {
    if (err instanceof ApiError && err.status === 404 && /shared-access group/.test(err.message)) {
      await deployPrimaryWallet(signerAddress);
      return buildCall();
    }
    throw err;
  }
}
