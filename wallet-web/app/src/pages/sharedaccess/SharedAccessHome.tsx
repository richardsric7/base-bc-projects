// Closes wallet-web/PLAN.md §8's deferred "shared-access group wallets"
// v1 exclusion - the original's dashboard/sharedAccess/{landing,add}
// pages, folded into one tabbed page the same way pages/fund/FundWallet.tsx
// and pages/tokenize/TokenizeHome.tsx already fold their own multi-page
// originals. Per-group detail/actions live in GroupDetail.tsx; pending
// approvals live in Approvals.tsx.
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { listWallets, createGroup, type WalletSummary, type GroupRole } from '../../api/sharedAccessApi';

type Tab = 'wallets' | 'new';

export default function SharedAccessHome() {
  const [tab, setTab] = useState<Tab>('wallets');
  const isOnline = useIsOnline();

  return (
    <div className="max-w-lg space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Shared access</h2>
        <Link to="/shared-access/approvals" className="text-primary-700 text-sm underline">
          Approvals
        </Link>
      </div>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to manage shared wallets.
        </p>
      )}
      <div className="flex gap-2">
        {(['wallets', 'new'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-3 py-2 rounded-md font-montserratMedium text-sm ${
              tab === t ? 'bg-primary-100 text-primary-800' : 'text-primary-700 hover:bg-primary-100'
            }`}
          >
            {t === 'wallets' ? 'My wallets' : 'Create group'}
          </button>
        ))}
      </div>
      {tab === 'wallets' && <MyWallets />}
      {tab === 'new' && <NewGroupForm disabled={!isOnline} />}
    </div>
  );
}

function MyWallets() {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [wallets, setWallets] = useState<WalletSummary[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!primaryAddress) return;
    listWallets(primaryAddress)
      .then(setWallets)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [primaryAddress]);

  if (error) return <p className="text-red-500 text-sm">{error}</p>;
  if (!wallets) return <p className="text-gray-600 text-sm">Loading…</p>;

  return (
    <ul className="space-y-2">
      {wallets.map((w) => (
        <li key={w.address} className="bg-primary-100 rounded-lg p-4 flex items-center justify-between">
          <div>
            <p className="font-montserratSemiBold text-primary-800">
              {w.name || (w.kind === 'primary' ? 'Primary wallet' : `Group #${w.groupId}`)}
            </p>
            <p className="text-gray-600 text-xs break-all">{w.address}</p>
            <p className="text-gray-500 text-xs">
              {w.role}
              {w.kind === 'group' && ` · threshold ${w.threshold}`}
              {w.disabled && ' · disabled'}
            </p>
          </div>
          {w.kind === 'group' && w.groupId && (
            <Link to={`/shared-access/groups/${w.groupId}`} className="text-primary-700 text-sm underline">
              Manage
            </Link>
          )}
        </li>
      ))}
      {wallets.length === 0 && <p className="text-gray-600 text-sm">No wallets found.</p>}
    </ul>
  );
}

function NewGroupForm({ disabled }: { disabled: boolean }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [name, setName] = useState('');
  const [threshold, setThreshold] = useState('1');
  const [members, setMembers] = useState<{ address: string; role: GroupRole }[]>([
    { address: '', role: 'INITIATOR_APPROVER' },
  ]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [created, setCreated] = useState<number | null>(null);

  const updateMember = (i: number, patch: Partial<{ address: string; role: GroupRole }>) => {
    setMembers((prev) => prev.map((m, idx) => (idx === i ? { ...m, ...patch } : m)));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!primaryAddress) return;
    setError('');
    setBusy(true);
    try {
      const group = await createGroup(
        primaryAddress,
        name,
        Number(threshold),
        members.filter((m) => m.address),
      );
      setCreated(group.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <p className="text-gray-600 text-sm">
        Create a group wallet controlled by a threshold of its members' approvals - no single member can move funds
        alone.
      </p>
      <TextInput label="Group name" value={name} onChange={setName} placeholder="Family wallet" />
      <TextInput label="Approval threshold" value={threshold} onChange={setThreshold} placeholder="1" />
      <div className="space-y-2">
        <label className="text-primary-700 font-montserratMedium">Members</label>
        {members.map((m, i) => (
          <div key={i} className="flex gap-2">
            <input
              className="ring-1 ring-gray-200 focus:ring-primary-600 rounded-md flex-1 h-12 px-2"
              value={m.address}
              onChange={(e) => updateMember(i, { address: e.target.value })}
              placeholder="0x..."
            />
            <select
              className="ring-1 ring-gray-200 focus:ring-primary-600 rounded-md h-12 px-2"
              value={m.role}
              onChange={(e) => updateMember(i, { role: e.target.value as GroupRole })}
            >
              <option value="INITIATOR_APPROVER">Initiator + Approver</option>
              <option value="INITIATOR">Initiator only</option>
              <option value="APPROVER">Approver only</option>
              <option value="VIEW_ONLY">View only</option>
            </select>
          </div>
        ))}
        <button
          type="button"
          className="text-primary-700 text-sm underline"
          onClick={() => setMembers((prev) => [...prev, { address: '', role: 'APPROVER' }])}
        >
          + Add another member
        </button>
      </div>
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {created && (
        <p className="text-positive-primary text-sm">
          Group created.{' '}
          <Link to={`/shared-access/groups/${created}`} className="underline">
            Open it
          </Link>
        </p>
      )}
      <Button type="submit" label={busy ? 'Creating…' : 'Create group'} disabled={disabled || busy || !name} />
    </form>
  );
}
