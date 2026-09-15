import { useEffect, useState } from 'react';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { listCuratedTokens, type CuratedToken } from '../../api/assetsApi';
import { buildPayment, submitPayment } from '../../api/paymentsApi';
import { withWalletDeployRetry } from '../../api/usersApi';
import { signHexDigest } from '../../core/walletCoreClient';

// PLAN.md §6.4/§6.3: every step of this flow - not just the network
// calls inside it - is gated on confirmed connectivity. The button is
// disabled while offline, and buildPayment re-verifies reachability
// itself immediately before proceeding regardless.
export default function Send() {
  const isOnline = useIsOnline();
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);
  const signerUnlocked = useAppSelector((s) => s.wallet.signer.isUnlocked);
  const [tokens, setTokens] = useState<CuratedToken[]>([]);
  const [destination, setDestination] = useState('');
  const [amount, setAmount] = useState('');
  const [tokenAddress, setTokenAddress] = useState(''); // '' = native ETH
  const [status, setStatus] = useState<'idle' | 'building' | 'signing' | 'submitting' | 'done'>('idle');
  const [error, setError] = useState('');
  const [txHash, setTxHash] = useState('');

  useEffect(() => {
    listCuratedTokens()
      .then(setTokens)
      .catch(() => setTokens([]));
  }, []);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress || !signerAddress || !signerUnlocked) {
      setError('Unlock your signer wallet before sending a payment.');
      return;
    }
    setError('');
    setTxHash('');
    try {
      setStatus('building');
      // Proposes the transfer as a real Safe transaction against the
      // primary wallet (PLAN.md §17) and returns the digest to approve it.
      // Wrapped so an undeployed Safe (no shared-access group yet) is
      // deployed on the spot and the build is retried, rather than failing
      // outright - see usersApi.ts's withWalletDeployRetry.
      const proposal = await withWalletDeployRetry(signerAddress, () =>
        buildPayment(primaryAddress, destination, amount, tokenAddress || undefined),
      );

      setStatus('signing');
      // personal_sign over the digest's raw bytes with the signer's own
      // key - never the (nonexistent) primary wallet key, since the
      // primary wallet is a Safe with no private key of its own. Must be
      // signHexDigest, not signRequestMessage - see its doc comment.
      const signature = await signHexDigest('signer', proposal.digestToSign);

      setStatus('submitting');
      const idempotencyKey = crypto.randomUUID();
      const record = await submitPayment(
        primaryAddress,
        idempotencyKey,
        proposal.actionId,
        signature,
        destination,
        amount,
        tokenAddress || undefined,
      );

      setTxHash(record.txHash);
      setStatus('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus('idle');
    }
  };

  const busy = status !== 'idle' && status !== 'done';

  return (
    <div className="max-w-md space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Send</h2>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to send a payment.
        </p>
      )}
      <form onSubmit={handleSend} className="space-y-4">
        <TextInput label="Recipient address" value={destination} onChange={setDestination} placeholder="0x..." />
        <TextInput label="Amount (base units)" value={amount} onChange={setAmount} placeholder="1000000000000000000" />
        <div>
          <label className="text-primary-700 font-montserratMedium">Asset</label>
          <select
            className="mt-2 ring-1 md:ring-2 ring-gray-200 focus:ring-primary-600 rounded-md w-full h-12 px-2"
            value={tokenAddress}
            onChange={(e) => setTokenAddress(e.target.value)}
          >
            <option value="">ETH (native)</option>
            {tokens.map((t) => (
              <option key={t.contractAddress} value={t.contractAddress}>
                {t.symbol}
              </option>
            ))}
          </select>
        </div>
        {error && <p className="text-red-500 text-sm">{error}</p>}
        {status === 'done' && (
          <p className="text-positive-primary text-sm">Payment submitted. Tx hash: {txHash}</p>
        )}
        <Button
          type="submit"
          label={busy ? statusLabel(status) : 'Send payment'}
          disabled={!isOnline || busy || !destination || !amount}
        />
      </form>
    </div>
  );
}

function statusLabel(status: string): string {
  switch (status) {
    case 'building':
      return 'Building transaction…';
    case 'signing':
      return 'Signing…';
    case 'submitting':
      return 'Submitting…';
    default:
      return 'Send payment';
  }
}
