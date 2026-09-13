// Builds an EIP-4361 (Sign-In with Ethereum) message text - the exact
// format wallet-backend's siwe-go dependency parses server-side
// (internal/components/auth/services/services.go). PLAN.md §7's threat
// model leans on SIWE's own domain binding as a real phishing signal, so
// this must be the literal, unmodified spec format, not an approximation.
export interface SiweMessageParams {
  domain: string;
  address: string;
  statement: string;
  uri: string;
  chainId: number;
  nonce: string;
}

export function buildSiweMessage(params: SiweMessageParams): string {
  const issuedAt = new Date().toISOString();
  return [
    `${params.domain} wants you to sign in with your Ethereum account:`,
    params.address,
    '',
    params.statement,
    '',
    `URI: ${params.uri}`,
    'Version: 1',
    `Chain ID: ${params.chainId}`,
    `Nonce: ${params.nonce}`,
    `Issued At: ${issuedAt}`,
  ].join('\n');
}
