// Client for wallet-backend's tokenization component (PLAN.md §18/19):
// the asset-tokenization application/vetting/minting/sale pipeline, and
// the investor-facing purchase/interest/early-exit flows against a live
// sale. Every mutating on-chain step (application fee, crypto purchase,
// early exit) follows the same build -> personal_sign digest -> submit
// pattern as paymentsApi.ts/swapsApi.ts, per wallet-backend PLAN.md §18's
// fix delegating these to sharedaccess instead of a plain unsigned
// transaction a Safe wallet could never actually sign.
import { apiRequest } from './httpClient';

export interface ActionProposal {
  actionId: number;
  digestToSign: string;
}

export interface PurchaseProposal extends ActionProposal {
  paymentAmount?: string;
}

// Only the fields this app's UI actually reads/writes - the real backend
// row carries hundreds of asset-class-specific columns (PLAN.md §18's own
// note on why a field-for-field form is out of scope here); anything else
// round-trips through the object untouched since this app never
// overwrites fields it didn't set.
export interface TokenizedAsset {
  id: number;
  status: string;
  vettingStatus: boolean;
  assetSector: string;
  assetSubSector: string;
  assetType: string;
  assetName: string;
  assetCode: string;
  assetDescription: string;
  assetCountryLocation: string;
  assetQuoteCurrency: string;
  numberOfTokenToBeIssued: string;
  maxNumberOfTokenAvailableForSale: string;
  pricePerToken: string;
  assetDecimals: number;
  currentNavPerToken?: string;
  earlyExitPenaltyPercent?: string;
  earlyExitFeePercent?: string;
  tokenizationFeeId?: number | null;
  tokenizationApplicationFee?: string;
  tokenizationApplicationFeeAsset?: string;
  saleContractAddress?: string | null;
  issuerContractAddress?: string | null;
  createdAt: string;
  [key: string]: unknown;
}

export interface NewApplicationInput {
  assetSector: string;
  assetSubSector: string;
  assetType: string;
  assetName: string;
  assetCode: string;
  assetDescription: string;
  assetCountryLocation: string;
  assetQuoteCurrency: string;
  numberOfTokenToBeIssued: string;
  maxNumberOfTokenAvailableForSale: string;
  pricePerToken: string;
  assetDecimals: number;
}

export interface TokenizedAssetSubscription {
  id: string;
  tokenizedAssetId: number;
  quantity: string;
  paymentAmount: string;
  paymentAssetSymbol: string;
  txHash: string;
  channel: string;
  createdAt: string;
}

export interface ExpressionOfInterest {
  id: number;
  tokenizedAssetId: number;
  amount: string;
  createdAt: string;
}

export interface TokenizedAssetEarlyExit {
  id: number;
  tokenizedAssetId: number;
  tokenQuantityExited: string;
  payoutCurrency: string;
  payoutPricePerToken: string;
  estimatedPayoutAmount: string;
  burnTxHash: string;
  createdAt: string;
}

// Sector/sub-sector ids are the display strings themselves (e.g.
// "REAL_ESTATE") - the backend's reference tables carry no separate name
// column (PLAN.md §4.9's stakeholder-directory pattern).
export interface ReferenceData {
  sectors: { id: string }[];
  subSectors: { id: string; assetSectorId: string }[];
  currencies: { symbol: string; label: string }[];
  allowedCountries: { countryCode: string }[];
}

export interface TokenizedAssetType {
  id: number;
  assetSubSectorId: string;
  assetType: string;
}

export function getPublicReferenceData(): Promise<ReferenceData> {
  return apiRequest<ReferenceData>('/v1/tokenization/public');
}

export function getFullReferenceData(walletAddress: string): Promise<ReferenceData> {
  return apiRequest<ReferenceData>('/v1/tokenization/reference', { walletAddress });
}

export function getTypesBySubSector(walletAddress: string, subSectorId: string): Promise<TokenizedAssetType[]> {
  return apiRequest<TokenizedAssetType[]>(`/v1/tokenization/types?subSectorId=${encodeURIComponent(subSectorId)}`, {
    walletAddress,
  });
}

export function submitApplication(walletAddress: string, input: NewApplicationInput): Promise<TokenizedAsset> {
  return apiRequest<TokenizedAsset>('/v1/tokenization', { method: 'POST', walletAddress, body: input });
}

export function listMyApplications(walletAddress: string): Promise<TokenizedAsset[]> {
  return apiRequest<TokenizedAsset[]>('/v1/tokenization/applications', { walletAddress });
}

export function getApplication(walletAddress: string, assetId: number): Promise<TokenizedAsset> {
  return apiRequest<TokenizedAsset>(`/v1/tokenization/${assetId}`, { walletAddress });
}

export function deleteApplication(walletAddress: string, assetId: number): Promise<void> {
  return apiRequest<void>(`/v1/tokenization/${assetId}`, { method: 'DELETE', walletAddress });
}

// confirmApplication moves DRAFT -> APPLICATION_CONFIRMED and, if a fee is
// owed, returns a proposal for it; feePayment is null when no fee applies.
export function confirmApplication(
  walletAddress: string,
  assetId: number,
): Promise<{ asset: TokenizedAsset; feePayment: ActionProposal | null }> {
  return apiRequest(`/v1/tokenization/${assetId}/confirm`, { method: 'PUT', walletAddress });
}

export function submitApplicationFee(walletAddress: string, assetId: number, actionId: number, signature: string): Promise<void> {
  return apiRequest<void>(`/v1/tokenization/${assetId}/confirm/pay-fee`, {
    method: 'POST',
    walletAddress,
    body: { actionId, signature },
  });
}

export function confirmFeePayment(walletAddress: string, assetId: number): Promise<TokenizedAsset> {
  return apiRequest<TokenizedAsset>(`/v1/tokenization/${assetId}/fee/confirm`, { method: 'POST', walletAddress });
}

export function buildCryptoPurchase(walletAddress: string, assetId: number, quantity: string): Promise<PurchaseProposal> {
  return apiRequest<PurchaseProposal>(`/v1/tokenization/${assetId}/subscribe`, {
    method: 'POST',
    walletAddress,
    body: { quantity },
  });
}

export function confirmCryptoPurchase(
  walletAddress: string,
  assetId: number,
  quantity: string,
  actionId: number,
  signature: string,
): Promise<TokenizedAssetSubscription> {
  return apiRequest<TokenizedAssetSubscription>(`/v1/tokenization/${assetId}/subscribe/confirm`, {
    method: 'POST',
    walletAddress,
    body: { quantity, actionId, signature },
  });
}

export function listMySubscriptions(walletAddress: string): Promise<TokenizedAssetSubscription[]> {
  return apiRequest<TokenizedAssetSubscription[]>('/v1/tokenization/subscriptions/mine', { walletAddress });
}

export function expressInterest(walletAddress: string, assetId: number, amount?: string): Promise<ExpressionOfInterest> {
  return apiRequest<ExpressionOfInterest>(`/v1/tokenization/${assetId}/interest`, {
    method: 'POST',
    walletAddress,
    body: { amount },
  });
}

export function listMyInterest(walletAddress: string): Promise<ExpressionOfInterest[]> {
  return apiRequest<ExpressionOfInterest[]>('/v1/tokenization/interest/mine', { walletAddress });
}

export function buildEarlyExit(walletAddress: string, assetId: number, quantity: string): Promise<PurchaseProposal> {
  return apiRequest<PurchaseProposal>(`/v1/tokenization/${assetId}/early-exit`, {
    method: 'POST',
    walletAddress,
    body: { quantity },
  });
}

export function confirmEarlyExit(
  walletAddress: string,
  assetId: number,
  quantity: string,
  bankId: number,
  accountNumber: string,
  accountName: string,
  actionId: number,
  signature: string,
): Promise<TokenizedAssetEarlyExit> {
  return apiRequest<TokenizedAssetEarlyExit>(`/v1/tokenization/${assetId}/early-exit/confirm`, {
    method: 'POST',
    walletAddress,
    body: { quantity, bankId, accountNumber, accountName, actionId, signature },
  });
}

export function listMyEarlyExits(walletAddress: string): Promise<TokenizedAssetEarlyExit[]> {
  return apiRequest<TokenizedAssetEarlyExit[]>('/v1/tokenization/early-exits/mine', { walletAddress });
}
