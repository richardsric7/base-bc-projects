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

type WalletFilter = 'mine' | 'sharedWithMe';

// A small "shared with others" badge - shown only on a wallet the caller
// owns (isOwner) that also has more than one member (isShared), per
// wallet-backend PLAN.md §20's isOwner/isShared fields. Inline SVG rather
// than an image asset - no such icon exists to port from the original,
// and this app otherwise avoids pulling in an icon library for one glyph.
function SharedBadge() {
  return (
    <svg
      role="img"
      aria-label="Shared with others"
      viewBox="0 0 24 24"
      className="h-4 w-4 text-primary-600 shrink-0"
      fill="currentColor"
    >
      <title>Shared with others</title>
      <path d="M16 11c1.66 0 2.99-1.34 2.99-3S17.66 5 16 5c-1.66 0-3 1.34-3 3s1.34 3 3 3zm-8 0c1.66 0 2.99-1.34 2.99-3S9.66 5 8 5C6.34 5 5 6.34 5 8s1.34 3 3 3zm0 2c-2.33 0-7 1.17-7 3.5V19h14v-2.5c0-2.33-4.67-3.5-7-3.5zm8 0c-.29 0-.62.02-.97.05 1.16.84 1.97 1.97 1.97 3.45V19h6v-2.5c0-2.33-4.67-3.5-7-3.5z" />
    </svg>
  );
}

function MyWallets() {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [wallets, setWallets] = useState<WalletSummary[] | null>(null);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState<WalletFilter>('mine');

  useEffect(() => {
    if (!primaryAddress) return;
    listWallets(primaryAddress)
      .then(setWallets)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [primaryAddress]);

  if (error) return <p className="text-red-500 text-sm">{error}</p>;
  if (!wallets) return <p className="text-gray-600 text-sm">Loading…</p>;

  const filtered = wallets.filter((w) => (filter === 'mine' ? w.isOwner : !w.isOwner));

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        {(['mine', 'sharedWithMe'] as WalletFilter[]).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => setFilter(f)}
            className={`px-3 py-1.5 rounded-md font-montserratMedium text-xs ${
              filter === f ? 'bg-primary-800 text-white' : 'bg-primary-100 text-primary-700 hover:bg-primary-200'
            }`}
          >
            {f === 'mine' ? 'My Wallets' : 'Wallets Shared With Me'}
          </button>
        ))}
      </div>
      <ul className="space-y-2">
        {filtered.map((w) => (
          <li key={w.address} className="bg-primary-100 rounded-lg p-4 flex items-center justify-between">
            <div>
              <div className="flex items-center gap-1.5">
                <p className="font-montserratSemiBold text-primary-800">
                  {w.name || (w.kind === 'primary' ? 'Primary wallet' : `Group #${w.groupId}`)}
                </p>
                {w.isOwner && w.isShared && <SharedBadge />}
              </div>
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
        {filtered.length === 0 && (
          <p className="text-gray-600 text-sm">
            {filter === 'mine' ? 'No wallets found.' : 'No wallets have been shared with you.'}
          </p>
        )}
      </ul>
    </div>
  );
}

function NewGroupForm({ disabled }: { disabled: boolean }) {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const myUsername = useAppSelector((s) => s.auth.username);
  const [name, setName] = useState('');
  const [threshold, setThreshold] = useState('1');
  // The creator isn't implicitly added as a member server-side (wallet-
  // backend's CreateGroup only trusts/records who's actually listed) -
  // pre-fill the first row with the caller's own username, editable like
  // any other row, so an easy-to-miss step doesn't lock the creator out
  // of their own new group.
  const [members, setMembers] = useState<{ username: string; role: GroupRole }[]>([
    { username: myUsername ?? '', role: 'APPROVER' },
  ]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [created, setCreated] = useState<number | null>(null);

  const updateMember = (i: number, patch: Partial<{ username: string; role: GroupRole }>) => {
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
        members.filter((m) => m.username),
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
              value={m.username}
              onChange={(e) => updateMember(i, { username: e.target.value })}
              placeholder="username"
            />
            <select
              className="ring-1 ring-gray-200 focus:ring-primary-600 rounded-md h-12 px-2"
              value={m.role}
              onChange={(e) => updateMember(i, { role: e.target.value as GroupRole })}
            >
              <option value="APPROVER">Approver</option>
              <option value="INITIATOR">Initiator only</option>
              <option value="VIEW_ONLY">View only</option>
            </select>
          </div>
        ))}
        <button
          type="button"
          className="text-primary-700 text-sm underline"
          onClick={() => setMembers((prev) => [...prev, { username: '', role: 'APPROVER' }])}
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
