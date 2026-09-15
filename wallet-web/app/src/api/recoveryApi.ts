import { apiRequest } from './httpClient';
import type { User } from './usersApi';

// wallet-backend PLAN.md §15: two independent, coexisting recovery
// branches - Branch A (free, DB-only address swap, abandons funds/
// sub-wallets/shared-access at the old address) and Branch B (paid, true
// wallet-signer recovery via a Safe owner swap, preserves everything).
// A user may have neither, either, or both enabled.

export interface SecurityQuestion {
  id: number;
  question: string;
}

export interface SecurityAnswerInput {
  securityQuestionId: number;
  answer: string;
}

// GET /v1/users/security-questions (public) - the fixed catalog to pick
// from when setting up account-recovery questions.
export function listSecurityQuestions(): Promise<SecurityQuestion[]> {
  return apiRequest<SecurityQuestion[]>('/v1/users/security-questions');
}

// POST /v1/users/security-answers (SignatureAuth) - stores one hashed
// answer. Call once per question the user picks (typically 2-3).
export function setSecurityAnswer(
  walletAddress: string,
  securityQuestionId: number,
  answer: string,
): Promise<void> {
  return apiRequest<void>('/v1/users/security-answers', {
    method: 'POST',
    walletAddress,
    body: { securityQuestionId, answer },
  });
}

// POST /v1/users/security-answers/verify (SignatureAuth) - lets a user
// confirm they remember an answer correctly before relying on it later,
// entirely optionally (a UX nicety, not required by either recovery
// branch's execution flow).
export function verifySecurityAnswer(
  walletAddress: string,
  securityQuestionId: number,
  answer: string,
): Promise<boolean> {
  return apiRequest<{ match: boolean }>('/v1/users/security-answers/verify', {
    method: 'POST',
    walletAddress,
    body: { securityQuestionId, answer },
  }).then((r) => r.match);
}

// --- Branch A: free, DB-only address swap (recovery.go) ---------------

export function enableAccountRecovery(walletAddress: string): Promise<void> {
  return apiRequest<void>('/v1/users/account-recovery', { method: 'POST', walletAddress });
}

export function disableAccountRecovery(walletAddress: string): Promise<void> {
  return apiRequest<void>('/v1/users/account-recovery', { method: 'DELETE', walletAddress });
}

// POST /v1/account-recovery/:username/request-otp (public, unauthenticated
// by design - PLAN.md §15's recovery.go doc comment: the whole point is
// helping someone who can no longer produce a signature at all). Always
// responds success regardless of whether the username exists or has
// recovery enabled, so this can never be used to probe for either.
export function requestAccountRecoveryOTP(username: string): Promise<void> {
  return apiRequest<void>(`/v1/account-recovery/${encodeURIComponent(username)}/request-otp`, { method: 'POST' });
}

// Must match wallet-backend's services.RecoveryMessage(username,
// newAddress) exactly, byte-for-byte - this is a genuine human-composed
// string message, signed with signRequestMessage (NOT signHexDigest).
// Shared by both branches: Branch A signs it with the new *wallet*
// address's key, Branch B with the new *signer* address's key, but the
// message template itself is identical either way (recoverWallet calls
// the same verifyNewAddressOwnership helper internally - wallet-backend
// PLAN.md §15.9's recoverWallet doc comment).
export function buildRecoveryMessage(username: string, newAddress: string): string {
  return `wallet-backend account recovery\nusername: ${username}\nnew address: ${newAddress}`;
}

export interface AccountRecoveryLog {
  oldAddress: string;
  newAddress: string;
  createdAt: string;
}

// POST /v1/account-recovery/:username/recover (public). newAddressSignature
// must be a personal_sign (signRequestMessage, NOT signHexDigest - this is
// a genuine human-composed string, services.RecoveryMessage(username,
// newAddress)) produced by newAddress's own freshly-generated key, proving
// control of the address the account is being re-pointed to.
export function recoverAccount(
  username: string,
  newAddress: string,
  newAddressSignature: string,
  otp: string,
  answers: SecurityAnswerInput[],
): Promise<AccountRecoveryLog> {
  return apiRequest<AccountRecoveryLog>(`/v1/account-recovery/${encodeURIComponent(username)}/recover`, {
    method: 'POST',
    body: { newAddress, newAddressSignature, otp, answers },
  });
}

// --- Branch B: paid, true wallet-signer recovery (wallet_recovery.go) -

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

export interface EnableWalletRecoveryChallenge {
  addOwnerSafeTxHash: string;
  setGuardSafeTxHash: string;
  feeTx?: UnsignedTx;
  feeWalletAddress?: string;
  feeAmountWei?: string;
}

export function buildEnableWalletRecovery(walletAddress: string): Promise<EnableWalletRecoveryChallenge> {
  return apiRequest<EnableWalletRecoveryChallenge>('/v1/users/wallet-recovery/enable', {
    method: 'POST',
    walletAddress,
  });
}

// addOwnerSignature/setGuardSignature must be signHexDigest (NOT
// signRequestMessage) over the two SafeTxHash values - these are binary
// digests, not human-composed strings; see wallet-web/PLAN.md §15 for why
// that distinction matters.
export function confirmEnableWalletRecovery(
  walletAddress: string,
  feeTxHash: string,
  addOwnerSignature: string,
  setGuardSignature: string,
): Promise<User> {
  return apiRequest<User>('/v1/users/wallet-recovery/enable/confirm', {
    method: 'POST',
    walletAddress,
    body: { feeTxHash, addOwnerSignature, setGuardSignature },
  });
}

export interface DisableWalletRecoveryChallenge {
  removeOwnerSafeTxHash: string;
  clearGuardSafeTxHash: string;
}

export function buildDisableWalletRecovery(walletAddress: string): Promise<DisableWalletRecoveryChallenge> {
  return apiRequest<DisableWalletRecoveryChallenge>('/v1/users/wallet-recovery/disable', {
    method: 'POST',
    walletAddress,
  });
}

export function confirmDisableWalletRecovery(
  walletAddress: string,
  removeOwnerSignature: string,
  clearGuardSignature: string,
): Promise<User> {
  return apiRequest<User>('/v1/users/wallet-recovery/disable/confirm', {
    method: 'POST',
    walletAddress,
    body: { removeOwnerSignature, clearGuardSignature },
  });
}

export interface WalletRecoveryLog {
  walletAddress: string;
  oldSignerAddress: string;
  newSignerAddress: string;
  txHash: string;
  createdAt: string;
}

// POST /v1/account-recovery/:username/recover-wallet (public).
// newSignerAddressSignature must be signRequestMessage (a genuine string
// message, the same RecoveryMessage shape Branch A uses) produced by
// newSignerAddress's own freshly-generated key.
export function recoverWallet(
  username: string,
  newSignerAddress: string,
  newSignerAddressSignature: string,
  otp: string,
  answers: SecurityAnswerInput[],
): Promise<WalletRecoveryLog> {
  return apiRequest<WalletRecoveryLog>(`/v1/account-recovery/${encodeURIComponent(username)}/recover-wallet`, {
    method: 'POST',
    body: { newSignerAddress, newSignerAddressSignature, otp, answers },
  });
}
