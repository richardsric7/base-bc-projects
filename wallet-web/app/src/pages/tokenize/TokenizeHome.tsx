// Asset-tokenization hub - wallet-web/PLAN.md §19's closure of the
// "asset tokenization" gap: the original app's `tokenize/apply` issuer
// flow and `tokenizedAssets`/`subscriptions.mine` investor views. This
// covers the core deal-terms fields (PLAN.md §19's own scoping note: the
// real backend row carries hundreds of asset-class-specific columns -
// e.g. bond/fund/commodity-specific fields - which stay a deliberate
// follow-up rather than a field-for-field form here) and the full
// application lifecycle/investor workflow against them.
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import {
  listMyApplications,
  submitApplication,
  deleteApplication,
  listMySubscriptions,
  listMyInterest,
  listMyEarlyExits,
  type TokenizedAsset,
  type TokenizedAssetSubscription,
  type ExpressionOfInterest,
  type TokenizedAssetEarlyExit,
} from '../../api/tokenizationApi';

type Tab = 'applications' | 'new' | 'investments';

export default function TokenizeHome() {
  const [tab, setTab] = useState<Tab>('applications');

  return (
    <div className="max-w-2xl space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Asset tokenization</h2>
      <div className="flex gap-2">
        {(['applications', 'new', 'investments'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-3 py-2 rounded-md font-montserratMedium text-sm ${
              tab === t ? 'bg-primary-100 text-primary-800' : 'text-primary-700 hover:bg-primary-100'
            }`}
          >
            {t === 'applications' ? 'My applications' : t === 'new' ? 'New application' : 'My investments'}
          </button>
        ))}
      </div>
      {tab === 'applications' && <MyApplications />}
      {tab === 'new' && <NewApplicationForm onSubmitted={() => setTab('applications')} />}
      {tab === 'investments' && <MyInvestments />}
    </div>
  );
}

function MyApplications() {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [applications, setApplications] = useState<TokenizedAsset[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const load = () => {
    if (!primaryAddress) return;
    setLoading(true);
    listMyApplications(primaryAddress)
      .then(setApplications)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(load, [primaryAddress]);

  const handleDelete = async (assetId: number) => {
    if (!primaryAddress) return;
    try {
      await deleteApplication(primaryAddress, assetId);
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  if (loading) return <p className="text-gray-500 text-sm">Loading…</p>;
  if (error) return <p className="text-red-500 text-sm">{error}</p>;
  if (applications.length === 0) return <p className="text-gray-500 text-sm">No applications yet.</p>;

  return (
    <ul className="space-y-2">
      {applications.map((a) => (
        <li key={a.id} className="border border-gray-200 rounded-md p-3 flex items-center justify-between">
          <div>
            <Link to={`/tokenize/${a.id}`} className="text-primary-800 font-montserratMedium">
              {a.assetName || `Asset #${a.id}`}
            </Link>
            <p className="text-xs text-gray-500">
              {a.assetCode} · {a.status}
            </p>
          </div>
          {a.status === 'DRAFT' && (
            <button type="button" className="text-trovored-primary text-sm" onClick={() => handleDelete(a.id)}>
              Delete
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

function NewApplicationForm({ onSubmitted }: { onSubmitted: () => void }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [assetSector, setAssetSector] = useState('');
  const [assetSubSector, setAssetSubSector] = useState('');
  const [assetType, setAssetType] = useState('');
  const [assetName, setAssetName] = useState('');
  const [assetCode, setAssetCode] = useState('');
  const [assetDescription, setAssetDescription] = useState('');
  const [assetCountryLocation, setAssetCountryLocation] = useState('NG');
  const [assetQuoteCurrency, setAssetQuoteCurrency] = useState('USDC');
  const [numberOfTokenToBeIssued, setNumberOfTokenToBeIssued] = useState('');
  const [maxNumberOfTokenAvailableForSale, setMaxNumberOfTokenAvailableForSale] = useState('');
  const [pricePerToken, setPricePerToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setError('');
    setBusy(true);
    try {
      await submitApplication(primaryAddress, {
        assetSector,
        assetSubSector,
        assetType,
        assetName,
        assetCode,
        assetDescription,
        assetCountryLocation,
        assetQuoteCurrency,
        numberOfTokenToBeIssued,
        maxNumberOfTokenAvailableForSale,
        pricePerToken,
        assetDecimals: 2,
      });
      onSubmitted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <p className="text-gray-600 text-sm">
        Core deal terms only - detailed asset-class-specific fields (bond/fund/commodity documentation, stakeholder
        directory, etc.) are completed with the vetting team after submission.
      </p>
      <TextInput label="Asset name" value={assetName} onChange={setAssetName} placeholder="Trovo Gold Trust" />
      <TextInput label="Asset code" value={assetCode} onChange={setAssetCode} placeholder="TGLD" />
      <TextInput label="Description" value={assetDescription} onChange={setAssetDescription} placeholder="A gold-backed asset" />
      <TextInput label="Sector" value={assetSector} onChange={setAssetSector} placeholder="COMMODITY" />
      <TextInput label="Sub-sector" value={assetSubSector} onChange={setAssetSubSector} placeholder="PRECIOUS_METALS" />
      <TextInput label="Type" value={assetType} onChange={setAssetType} placeholder="GOLD" />
      <TextInput label="Country (ISO 2-letter)" value={assetCountryLocation} onChange={setAssetCountryLocation} placeholder="NG" />
      <TextInput label="Quote currency" value={assetQuoteCurrency} onChange={setAssetQuoteCurrency} placeholder="USDC" />
      <TextInput
        label="Tokens to issue"
        value={numberOfTokenToBeIssued}
        onChange={setNumberOfTokenToBeIssued}
        placeholder="1000"
      />
      <TextInput
        label="Tokens available for sale"
        value={maxNumberOfTokenAvailableForSale}
        onChange={setMaxNumberOfTokenAvailableForSale}
        placeholder="600"
      />
      <TextInput label="Price per token" value={pricePerToken} onChange={setPricePerToken} placeholder="2.50" />
      {error && <p className="text-red-500 text-sm">{error}</p>}
      <Button type="submit" label={busy ? 'Submitting…' : 'Submit application'} disabled={busy || !assetName || !assetCode} />
    </form>
  );
}

function MyInvestments() {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [subscriptions, setSubscriptions] = useState<TokenizedAssetSubscription[]>([]);
  const [interest, setInterest] = useState<ExpressionOfInterest[]>([]);
  const [exits, setExits] = useState<TokenizedAssetEarlyExit[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!primaryAddress) return;
    Promise.all([listMySubscriptions(primaryAddress), listMyInterest(primaryAddress), listMyEarlyExits(primaryAddress)])
      .then(([subs, ints, exs]) => {
        setSubscriptions(subs);
        setInterest(ints);
        setExits(exs);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [primaryAddress]);

  if (error) return <p className="text-red-500 text-sm">{error}</p>;

  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-primary-700 font-montserratMedium text-sm mb-2">Purchases</h3>
        {subscriptions.length === 0 ? (
          <p className="text-gray-500 text-sm">None yet.</p>
        ) : (
          <ul className="space-y-1 text-sm">
            {subscriptions.map((s) => (
              <li key={s.id}>
                {s.quantity} tokens (asset #{s.tokenizedAssetId}) for {s.paymentAmount} {s.paymentAssetSymbol} via {s.channel}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h3 className="text-primary-700 font-montserratMedium text-sm mb-2">Expressions of interest</h3>
        {interest.length === 0 ? (
          <p className="text-gray-500 text-sm">None yet.</p>
        ) : (
          <ul className="space-y-1 text-sm">
            {interest.map((i) => (
              <li key={i.id}>
                Asset #{i.tokenizedAssetId}
                {i.amount ? ` - ${i.amount}` : ''}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h3 className="text-primary-700 font-montserratMedium text-sm mb-2">Early exits</h3>
        {exits.length === 0 ? (
          <p className="text-gray-500 text-sm">None yet.</p>
        ) : (
          <ul className="space-y-1 text-sm">
            {exits.map((e) => (
              <li key={e.id}>
                Asset #{e.tokenizedAssetId}: {e.tokenQuantityExited} tokens, est. payout {e.estimatedPayoutAmount}{' '}
                {e.payoutCurrency}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
