// Fiat/Stablerail/crypto wallet-funding hub - wallet-web/PLAN.md §19's
// closure of the "Deposit/Withdraw" gap: the original app's
// walletOperations.tsx bundled these into modals on the dashboard; this
// port gives each its own tab on a dedicated page instead. Withdrawal is
// the one path here that moves the user's own on-chain funds, so it
// alone follows the build -> personal_sign digest -> confirm pattern
// (see cryptoApi.ts's doc comment); fiat/stablerail top-ups and crypto
// deposits never touch the user's signer key at all.
import { useState } from 'react';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { signHexDigest } from '../../core/walletCoreClient';
import { createFiatInvoice } from '../../api/fiatApi';
import { listBanks, initiateOnboarding, initiateOnramp, type Bank } from '../../api/stablerailApi';
import {
  getDepositAddresses,
  getWithdrawalNetworks,
  buildWithdrawal,
  confirmWithdrawal,
  type DepositAddress,
  type WithdrawalNetworksResult,
} from '../../api/cryptoApi';

type Tab = 'fiat' | 'naira' | 'crypto';

export default function FundWallet() {
  const [tab, setTab] = useState<Tab>('fiat');
  const isOnline = useIsOnline();

  return (
    <div className="max-w-md space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Fund wallet</h2>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to fund your wallet.
        </p>
      )}
      <div className="flex gap-2">
        {(['fiat', 'naira', 'crypto'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-3 py-2 rounded-md font-montserratMedium text-sm ${
              tab === t ? 'bg-primary-100 text-primary-800' : 'text-primary-700 hover:bg-primary-100'
            }`}
          >
            {t === 'fiat' ? 'Card / Bank (Flutterwave)' : t === 'naira' ? 'Naira (Stablerail)' : 'Crypto'}
          </button>
        ))}
      </div>
      {tab === 'fiat' && <FiatTab disabled={!isOnline} />}
      {tab === 'naira' && <NairaTab disabled={!isOnline} />}
      {tab === 'crypto' && <CryptoTab disabled={!isOnline} />}
    </div>
  );
}

function FiatTab({ disabled }: { disabled: boolean }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [amount, setAmount] = useState('');
  const [currency, setCurrency] = useState('NGN');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [invoiceRef, setInvoiceRef] = useState('');

  const handleTopUp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setError('');
    setBusy(true);
    try {
      const reference = crypto.randomUUID();
      const invoice = await createFiatInvoice(primaryAddress, reference, 'topup', Number(amount), currency);
      setInvoiceRef(invoice.reference);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleTopUp} className="space-y-4">
      <p className="text-gray-600 text-sm">Top up via Flutterwave (card, bank transfer, or mobile money).</p>
      <TextInput label="Amount" value={amount} onChange={setAmount} placeholder="50" />
      <TextInput label="Currency" value={currency} onChange={setCurrency} placeholder="NGN" />
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {invoiceRef && (
        <p className="text-positive-primary text-sm">
          Invoice created ({invoiceRef}) - complete payment via the emailed Flutterwave link.
        </p>
      )}
      <Button type="submit" label={busy ? 'Creating invoice…' : 'Create invoice'} disabled={disabled || busy || !amount} />
    </form>
  );
}

function NairaTab({ disabled }: { disabled: boolean }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [bvn, setBvn] = useState('');
  const [amount, setAmount] = useState('');
  const [banks, setBanks] = useState<Bank[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');

  const loadBanks = async () => {
    if (!primaryAddress) return;
    try {
      setBanks(await listBanks(primaryAddress));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleOnboard = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setError('');
    setBusy(true);
    try {
      const result = await initiateOnboarding(primaryAddress, bvn);
      setMessage(result.message);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleOnramp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setError('');
    setBusy(true);
    try {
      await initiateOnramp(primaryAddress, Number(amount));
      setMessage('Onramp initiated - cNGN will credit your wallet once the transfer clears.');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <form onSubmit={handleOnboard} className="space-y-4">
        <p className="text-gray-600 text-sm">Onboard your BVN once, then fund via bank transfer to receive cNGN.</p>
        <TextInput label="BVN (11 digits)" value={bvn} onChange={setBvn} placeholder="12345678901" />
        <Button type="submit" label={busy ? 'Submitting…' : 'Onboard BVN'} disabled={disabled || busy || bvn.length !== 11} />
      </form>
      <form onSubmit={handleOnramp} className="space-y-4">
        <TextInput label="Amount (NGN)" value={amount} onChange={setAmount} placeholder="5000" />
        <Button type="submit" label={busy ? 'Submitting…' : 'Initiate onramp'} disabled={disabled || busy || !amount} />
      </form>
      <button type="button" className="text-primary-700 text-sm underline" onClick={loadBanks}>
        Show supported banks
      </button>
      {banks && (
        <ul className="text-sm text-gray-600 list-disc pl-4">
          {banks.map((b) => (
            <li key={b.code}>{b.name}</li>
          ))}
        </ul>
      )}
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {message && <p className="text-positive-primary text-sm">{message}</p>}
    </div>
  );
}

function CryptoTab({ disabled }: { disabled: boolean }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);
  const [currency, setCurrency] = useState('USDC');
  const [depositAddresses, setDepositAddresses] = useState<DepositAddress[] | null>(null);
  const [error, setError] = useState('');

  const [network, setNetwork] = useState('base');
  const [amount, setAmount] = useState('');
  const [toAddress, setToAddress] = useState('');
  const [networksResult, setNetworksResult] = useState<WithdrawalNetworksResult | null>(null);
  const [status, setStatus] = useState<'idle' | 'building' | 'signing' | 'submitting' | 'done'>('idle');
  const [withdrawTxHash, setWithdrawTxHash] = useState('');

  const loadDeposit = async () => {
    if (!primaryAddress) return;
    setError('');
    try {
      setDepositAddresses(await getDepositAddresses(primaryAddress, currency));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const loadNetworks = async () => {
    if (!primaryAddress) return;
    setError('');
    try {
      setNetworksResult(await getWithdrawalNetworks(primaryAddress, currency));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleWithdraw = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress || !signerAddress) return;
    setError('');
    setWithdrawTxHash('');
    try {
      setStatus('building');
      const proposal = await buildWithdrawal(primaryAddress, currency, network, Number(amount));

      setStatus('signing');
      const signature = await signHexDigest('signer', proposal.digestToSign);

      setStatus('submitting');
      const request = await confirmWithdrawal(
        primaryAddress,
        currency,
        network,
        toAddress,
        Number(amount),
        proposal.actionId,
        signature,
      );
      setWithdrawTxHash(request.treasuryTxHash);
      setStatus('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus('idle');
    }
  };

  const busy = status !== 'idle' && status !== 'done';

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <TextInput label="Currency" value={currency} onChange={setCurrency} placeholder="USDC" />
        <div className="flex gap-2">
          <button type="button" className="text-primary-700 text-sm underline" onClick={loadDeposit}>
            Show deposit address
          </button>
          <button type="button" className="text-primary-700 text-sm underline" onClick={loadNetworks}>
            Show withdrawal networks
          </button>
        </div>
        {depositAddresses && depositAddresses.length > 0 && (
          <p className="text-sm text-gray-700 break-all">
            Deposit address ({depositAddresses[0].network}): {depositAddresses[0].address}
          </p>
        )}
        {networksResult && (
          <p className="text-sm text-gray-600">
            Treasury: {networksResult.treasuryAddress} · Networks: {networksResult.networks.map((n) => n.network).join(', ')}
          </p>
        )}
      </div>
      <form onSubmit={handleWithdraw} className="space-y-4">
        <p className="text-gray-600 text-sm">Withdraw to an external address (signs a real transfer from your wallet).</p>
        <TextInput label="Network" value={network} onChange={setNetwork} placeholder="base" />
        <TextInput label="Amount" value={amount} onChange={setAmount} placeholder="10" />
        <TextInput label="Destination address" value={toAddress} onChange={setToAddress} placeholder="0x... or bank details" />
        {withdrawTxHash && <p className="text-positive-primary text-sm">Withdrawal submitted. Tx hash: {withdrawTxHash}</p>}
        <Button
          type="submit"
          label={busy ? statusLabel(status) : 'Withdraw'}
          disabled={disabled || busy || !amount || !toAddress}
        />
      </form>
      {error && <p className="text-red-500 text-sm">{error}</p>}
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
      return 'Withdraw';
  }
}
