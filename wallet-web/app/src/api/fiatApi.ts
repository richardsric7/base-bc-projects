// Client for wallet-backend's fiat component (PLAN.md §18/19): Flutterwave-
// backed fiat top-up. Every route requires the wallet's own signed
// request, same as payments/assets.
import { apiRequest } from './httpClient';

export interface ActivationQuote {
  amount: number;
  currency: string;
}

export interface FiatInvoice {
  id: string;
  reference: string;
  paymentType: string;
  amount: number;
  currency: string;
  status: string;
  createdAt: string;
}

export function getActivationQuote(walletAddress: string): Promise<ActivationQuote> {
  return apiRequest<ActivationQuote>('/v1/fiat/activate', { walletAddress });
}

export function getFiatPayments(
  walletAddress: string,
): Promise<{ completedPayments: unknown[]; invoices: FiatInvoice[] }> {
  return apiRequest(`/v1/fiat/payments`, { walletAddress });
}

export function createFiatInvoice(
  walletAddress: string,
  reference: string,
  paymentType: string,
  amount: number,
  currency: string,
): Promise<FiatInvoice> {
  return apiRequest<FiatInvoice>('/v1/fiat/flutterwave/invoices', {
    method: 'POST',
    walletAddress,
    body: { reference, paymentType, amount, currency },
  });
}
