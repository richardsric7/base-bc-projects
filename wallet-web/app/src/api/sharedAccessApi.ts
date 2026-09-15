// Client for wallet-backend's /v1/shared-access/* (PLAN.md §20's closure
// of wallet-web/PLAN.md §8's deferred "shared-access group wallets" v1
// exclusion). Every route is SignatureAuth-protected but, unlike other
// domains, always authenticates the caller as *themselves* (their own
// primary wallet) rather than as the group being acted on - the target
// group is always named by its own :groupId path/body param instead, so
// every call here passes the caller's own primaryAddress as walletAddress
// (see wallet-backend's signature_auth.go: this is the "wallet == signer's
// own owned wallet" fast path, never the delegated-wallet case other
// domains like paymentsApi.ts use).
import { apiRequest } from './httpClient';

export type GroupRole = 'INITIATOR' | 'APPROVER' | 'VIEW_ONLY' | 'INITIATOR_APPROVER';

export interface ClosedGroup {
  id: number;
  name: string;
  purpose: string;
  address?: string;
  threshold?: number;
  disabled: boolean;
  createdAt: string;
}

export interface GroupMember {
  id: number;
  groupId: number;
  memberAddress: string;
  role: GroupRole;
  createdAt: string;
}

export interface WalletSummary {
  address: string;
  kind: 'primary' | 'group';
  role: string;
  threshold: number;
  disabled: boolean;
  groupId?: number;
  name?: string;
  // isOwner: true for the primary wallet, or a group this caller created.
  // false for a group someone else created and added this caller to -
  // "shared with me". isShared: true when the underlying wallet has more
  // than one member - combined with isOwner, "a wallet of mine I've
  // shared with others" (wallet-backend PLAN.md §20).
  isOwner: boolean;
  isShared: boolean;
}

export interface CuratedBalance {
  symbol: string;
  contractAddress: string;
  decimals: number;
  balance: string;
}

export type ActionKind =
  | 'payment'
  | 'swap'
  | 'contract_call'
  | 'add_member'
  | 'remove_member'
  | 'change_threshold'
  | 'disable_group';

export type ActionStatus = 'PENDING' | 'SUBMITTED' | 'EXECUTED' | 'REJECTED';

export interface PendingAction {
  id: number;
  groupId: number;
  proposerAddress: string;
  kind: ActionKind;
  description: string;
  to: string;
  tokenAddress: string;
  value: string;
  data: string;
  requiredApprovals: number;
  safeNonce: string;
  status: ActionStatus;
  txHash: string;
  targetMemberAddress?: string;
  targetRole?: GroupRole;
  newThreshold?: number;
  rejectionReason: string;
  createdAt: string;
}

export interface ActionDetail extends PendingAction {
  digestToSign: string;
}

export function listWallets(primaryAddress: string): Promise<WalletSummary[]> {
  return apiRequest<WalletSummary[]>('/v1/shared-access/wallets', { walletAddress: primaryAddress });
}

export function createGroup(
  primaryAddress: string,
  name: string,
  threshold: number,
  members: { address: string; role: GroupRole }[],
): Promise<ClosedGroup> {
  return apiRequest<ClosedGroup>('/v1/shared-access/groups', {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { name, threshold, members },
  });
}

export function getGroup(primaryAddress: string, groupId: number): Promise<ClosedGroup> {
  return apiRequest<ClosedGroup>(`/v1/shared-access/groups/${groupId}`, { walletAddress: primaryAddress });
}

export function listMembers(primaryAddress: string, groupId: number): Promise<GroupMember[]> {
  return apiRequest<GroupMember[]>(`/v1/shared-access/groups/${groupId}/members`, {
    walletAddress: primaryAddress,
  });
}

export async function getBalance(primaryAddress: string, groupId: number, tokenAddress?: string): Promise<string> {
  const query = tokenAddress ? `?token=${tokenAddress}` : '';
  const result = await apiRequest<{ balance: string }>(`/v1/shared-access/balance/${groupId}${query}`, {
    walletAddress: primaryAddress,
  });
  return result.balance;
}

export function getCuratedBalances(primaryAddress: string, groupId: number): Promise<CuratedBalance[]> {
  return apiRequest<CuratedBalance[]>(`/v1/shared-access/balance/${groupId}/curated`, {
    walletAddress: primaryAddress,
  });
}

export function proposePayment(
  primaryAddress: string,
  groupId: number,
  description: string,
  recipient: string,
  amount: string,
  tokenAddress?: string,
): Promise<PendingAction> {
  return apiRequest<PendingAction>('/v1/shared-access/actions', {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { groupId, kind: 'payment', description, recipient, amount, tokenAddress: tokenAddress ?? '' },
  });
}

export function proposeContractCall(
  primaryAddress: string,
  groupId: number,
  description: string,
  to: string,
  valueWei: string,
  data: string,
): Promise<PendingAction> {
  return apiRequest<PendingAction>('/v1/shared-access/actions', {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { groupId, kind: 'contract_call', description, to, valueWei, data },
  });
}

export function listPending(primaryAddress: string): Promise<PendingAction[]> {
  return apiRequest<PendingAction[]>('/v1/shared-access/actions', { walletAddress: primaryAddress });
}

export function getAction(primaryAddress: string, actionId: number): Promise<ActionDetail> {
  return apiRequest<ActionDetail>(`/v1/shared-access/actions/${actionId}`, { walletAddress: primaryAddress });
}

export function approveAction(
  primaryAddress: string,
  actionId: number,
  signature: string,
): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/actions/${actionId}/approve`, {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { signature },
  });
}

export function rejectAction(primaryAddress: string, actionId: number, reason: string): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/actions/${actionId}/reject`, {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { reason },
  });
}

export function proposeAddMember(
  primaryAddress: string,
  groupId: number,
  address: string,
  role: GroupRole,
  newThreshold: number,
): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/groups/${groupId}/members`, {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { address, role, newThreshold },
  });
}

export function proposeRemoveMember(
  primaryAddress: string,
  groupId: number,
  memberAddress: string,
  newThreshold: number,
): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/groups/${groupId}/members/${memberAddress}/remove`, {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { newThreshold },
  });
}

export function proposeChangeThreshold(
  primaryAddress: string,
  groupId: number,
  newThreshold: number,
): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/groups/${groupId}/threshold`, {
    method: 'POST',
    walletAddress: primaryAddress,
    body: { newThreshold },
  });
}

export function proposeDisableGroup(primaryAddress: string, groupId: number): Promise<PendingAction> {
  return apiRequest<PendingAction>(`/v1/shared-access/groups/${groupId}/disable`, {
    method: 'POST',
    walletAddress: primaryAddress,
  });
}
