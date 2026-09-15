// A single tokenized asset: issuer-side application actions (confirm,
// pay the application fee, confirm fee payment once vetted) and
// investor-side actions (subscribe with crypto, express interest, early
// exit) - see TokenizeHome.tsx's doc comment for scope. Fee payment and
// crypto purchase/early-exit all sign a real digest (PLAN.md §18's fix),
// same pattern as Send.tsx.
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { signHexDigest } from '../../core/walletCoreClient';
import {
  getApplication,
  confirmApplication,
  submitApplicationFee,
  confirmFeePayment,
  buildCryptoPurchase,
  confirmCryptoPurchase,
  expressInterest,
  buildEarlyExit,
  confirmEarlyExit,
  type TokenizedAsset,
} from '../../api/tokenizationApi';

export default function AssetDetail() {
  const { assetId } = useParams<{ assetId: string }>();
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);
  const [asset, setAsset] = useState<TokenizedAsset | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const load = () => {
    if (!primaryAddress || !assetId) return;
    getApplication(primaryAddress, Number(assetId))
      .then(setAsset)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  };

  useEffect(load, [primaryAddress, assetId]);

  if (!asset) return <p className="text-gray-500 text-sm">{error || 'Loading…'}</p>;

  const handleConfirmApplication = async () => {
    if (!primaryAddress || !signerAddress || !assetId) return;
    setError('');
    try {
      const { feePayment } = await confirmApplication(primaryAddress, Number(assetId));
      if (feePayment) {
        const signature = await signHexDigest('signer', feePayment.digestToSign);
        await submitApplicationFee(primaryAddress, Number(assetId), feePayment.actionId, signature);
      }
      setNotice('Application confirmed.');
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleConfirmFeePayment = async () => {
    if (!primaryAddress || !assetId) return;
    setError('');
    try {
      await confirmFeePayment(primaryAddress, Number(assetId));
      setNotice('Fee payment confirmed.');
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="max-w-md space-y-6">
      <div>
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">{asset.assetName || `Asset #${asset.id}`}</h2>
        <p className="text-sm text-gray-500">
          {asset.assetCode} · {asset.status} {asset.vettingStatus ? '· vetted' : ''}
        </p>
        <p className="text-sm text-gray-600 mt-2">{asset.assetDescription}</p>
        <p className="text-sm text-gray-600">
          Price per token: {asset.pricePerToken} {asset.assetQuoteCurrency}
        </p>
      </div>

      {error && <p className="text-red-500 text-sm">{error}</p>}
      {notice && <p className="text-positive-primary text-sm">{notice}</p>}

      {asset.status === 'DRAFT' && <Button label="Confirm application" onClick={handleConfirmApplication} />}
      {asset.status === 'APPLICATION_CONFIRMED' && asset.vettingStatus && (
        <Button label="Confirm fee payment" onClick={handleConfirmFeePayment} />
      )}
      {asset.status === 'APPLICATION_CONFIRMED' && !asset.vettingStatus && (
        <p className="text-gray-500 text-sm">Awaiting vetting before fee payment can be confirmed.</p>
      )}

      {(asset.status === 'PRIMARY_SALE_ACTIVE' || asset.status === 'SECONDARY_SALE_ACTIVE') && (
        <>
          <SubscribeSection assetId={asset.id} onDone={setNotice} onError={setError} />
          <InterestSection assetId={asset.id} onDone={setNotice} onError={setError} />
          <EarlyExitSection assetId={asset.id} onDone={setNotice} onError={setError} />
        </>
      )}
    </div>
  );
}

type SectionProps = { assetId: number; onDone: (msg: string) => void; onError: (msg: string) => void };

function SubscribeSection({ assetId, onDone, onError }: SectionProps) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [quantity, setQuantity] = useState('');
  const [busy, setBusy] = useState(false);

  const handleSubscribe = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setBusy(true);
    try {
      const proposal = await buildCryptoPurchase(primaryAddress, assetId, quantity);
      const signature = await signHexDigest('signer', proposal.digestToSign);
      await confirmCryptoPurchase(primaryAddress, assetId, quantity, proposal.actionId, signature);
      onDone('Purchase submitted.');
      setQuantity('');
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleSubscribe} className="space-y-3 border-t border-gray-100 pt-4">
      <h3 className="text-primary-700 font-montserratMedium text-sm">Subscribe (crypto)</h3>
      <TextInput label="Quantity" value={quantity} onChange={setQuantity} placeholder="10" />
      <Button type="submit" label={busy ? 'Submitting…' : 'Buy'} disabled={busy || !quantity} />
    </form>
  );
}

function InterestSection({ assetId, onDone, onError }: SectionProps) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [amount, setAmount] = useState('');
  const [busy, setBusy] = useState(false);

  const handleExpress = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setBusy(true);
    try {
      await expressInterest(primaryAddress, assetId, amount || undefined);
      onDone('Interest recorded.');
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleExpress} className="space-y-3 border-t border-gray-100 pt-4">
      <h3 className="text-primary-700 font-montserratMedium text-sm">Express interest</h3>
      <TextInput label="Indicative amount (optional)" value={amount} onChange={setAmount} placeholder="500" />
      <Button type="submit" label={busy ? 'Submitting…' : 'Express interest'} disabled={busy} />
    </form>
  );
}

function EarlyExitSection({ assetId, onDone, onError }: SectionProps) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [quantity, setQuantity] = useState('');
  const [bankId, setBankId] = useState('');
  const [accountNumber, setAccountNumber] = useState('');
  const [accountName, setAccountName] = useState('');
  const [busy, setBusy] = useState(false);

  const handleExit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setBusy(true);
    try {
      const proposal = await buildEarlyExit(primaryAddress, assetId, quantity);
      const signature = await signHexDigest('signer', proposal.digestToSign);
      await confirmEarlyExit(primaryAddress, assetId, quantity, Number(bankId), accountNumber, accountName, proposal.actionId, signature);
      onDone('Early exit submitted.');
      setQuantity('');
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleExit} className="space-y-3 border-t border-gray-100 pt-4">
      <h3 className="text-primary-700 font-montserratMedium text-sm">Early exit</h3>
      <TextInput label="Quantity" value={quantity} onChange={setQuantity} placeholder="5" />
      <TextInput label="Bank ID" value={bankId} onChange={setBankId} placeholder="1" />
      <TextInput label="Account number" value={accountNumber} onChange={setAccountNumber} placeholder="0123456789" />
      <TextInput label="Account name" value={accountName} onChange={setAccountName} placeholder="Jane Doe" />
      <Button
        type="submit"
        label={busy ? 'Submitting…' : 'Exit early'}
        disabled={busy || !quantity || !bankId || !accountNumber || !accountName}
      />
    </form>
  );
}
