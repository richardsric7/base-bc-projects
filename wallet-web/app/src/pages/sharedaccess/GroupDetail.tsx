// The original's dashboard/sharedAccess/walletInfo.tsx and update.tsx,
// folded into one page (see SharedAccessHome.tsx's own note on this
// pattern) - member-management proposals (add/remove member, change
// threshold, disable group) go through the exact same propose/approve
// pipeline as a payment (wallet-backend PLAN.md §13.10 Phase 5), so
// there's nothing structurally different about "update" that would
// justify its own route once both are on-screen together.
import { useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { signHexDigest } from '../../core/walletCoreClient';
import {
  getGroup,
  listMembers,
  getBalance,
  getCuratedBalances,
  proposePayment,
  proposeAddMember,
  proposeRemoveMember,
  proposeChangeThreshold,
  proposeDisableGroup,
  getAction,
  approveAction,
  type ClosedGroup,
  type GroupMember,
  type CuratedBalance,
  type GroupRole,
} from '../../api/sharedAccessApi';

export default function GroupDetail() {
  const { groupId } = useParams<{ groupId: string }>();
  const id = Number(groupId);
  const navigate = useNavigate();
  const isOnline = useIsOnline();
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);

  const [group, setGroup] = useState<ClosedGroup | null>(null);
  const [members, setMembers] = useState<GroupMember[]>([]);
  const [nativeBalance, setNativeBalance] = useState('');
  const [curated, setCurated] = useState<CuratedBalance[]>([]);
  const [error, setError] = useState('');

  const reload = () => {
    if (!primaryAddress) return;
    Promise.all([
      getGroup(primaryAddress, id),
      listMembers(primaryAddress, id),
      getBalance(primaryAddress, id),
      getCuratedBalances(primaryAddress, id).catch(() => []),
    ])
      .then(([g, m, bal, cur]) => {
        setGroup(g);
        setMembers(m);
        setNativeBalance(bal);
        setCurated(cur);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  };

  useEffect(reload, [primaryAddress, id]);

  // Proposing and immediately self-approving is only meaningful for a
  // 1-of-1 group (a plain sub-wallet, PLAN.md §13.4) or when this member
  // alone can already satisfy the threshold; for a genuine multi-party
  // group the proposal still needs the other members' own approvals via
  // Approvals.tsx regardless.
  const proposeAndSelfApprove = async (propose: () => Promise<{ id: number }>) => {
    if (!primaryAddress || !signerAddress) return;
    setError('');
    try {
      const action = await propose();
      const detail = await getAction(primaryAddress, action.id);
      const signature = await signHexDigest('signer', detail.digestToSign);
      await approveAction(primaryAddress, action.id, signature);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  if (error) return <p className="text-red-500 text-sm">{error}</p>;
  if (!group) return <p className="text-gray-600 text-sm">Loading…</p>;

  return (
    <div className="max-w-lg space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">{group.name}</h2>
        <button type="button" className="text-primary-700 text-sm underline" onClick={() => navigate('/shared-access')}>
          Back
        </button>
      </div>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to manage this wallet.
        </p>
      )}
      <div className="bg-primary-100 rounded-lg p-4 space-y-1 text-sm">
        <p className="break-all">Address: {group.address}</p>
        <p>Threshold: {group.threshold}</p>
        <p>Native balance: {nativeBalance}</p>
        {group.disabled && <p className="text-trovored-primary">Disabled</p>}
      </div>
      {curated.length > 0 && (
        <div className="bg-primary-100 rounded-lg p-4 space-y-1 text-sm">
          <p className="font-montserratSemiBold">Curated token balances</p>
          {curated.map((c) => (
            <p key={c.contractAddress} className="flex justify-between">
              <span>{c.symbol}</span>
              <span>{c.balance}</span>
            </p>
          ))}
        </div>
      )}
      <MembersSection members={members} />
      <ProposePaymentForm
        disabled={!isOnline || group.disabled}
        onPropose={(recipient, amount, tokenAddress) =>
          proposeAndSelfApprove(() => proposePayment(primaryAddress!, id, 'shared-access payment', recipient, amount, tokenAddress))
        }
      />
      <ManageMembersSection
        disabled={!isOnline || group.disabled}
        onAddMember={(username, role, newThreshold) =>
          proposeAndSelfApprove(() => proposeAddMember(primaryAddress!, id, username, role, newThreshold))
        }
        onRemoveMember={(username, newThreshold) =>
          proposeAndSelfApprove(() => proposeRemoveMember(primaryAddress!, id, username, newThreshold))
        }
        onChangeThreshold={(newThreshold) =>
          proposeAndSelfApprove(() => proposeChangeThreshold(primaryAddress!, id, newThreshold))
        }
        onDisable={() => proposeAndSelfApprove(() => proposeDisableGroup(primaryAddress!, id))}
      />
    </div>
  );
}

function MembersSection({ members }: { members: GroupMember[] }) {
  return (
    <div className="space-y-2">
      <p className="font-montserratSemiBold text-primary-800">Members</p>
      <ul className="space-y-1 text-sm">
        {members.map((m) => (
          <li key={m.id} className="flex justify-between bg-primary-100 rounded-md px-3 py-2">
            <span className="break-all">{m.memberAddress}</span>
            <span className="text-gray-600">{m.role}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ProposePaymentForm({
  disabled,
  onPropose,
}: {
  disabled: boolean;
  onPropose: (recipient: string, amount: string, tokenAddress?: string) => Promise<void>;
}) {
  const [recipient, setRecipient] = useState('');
  const [amount, setAmount] = useState('');
  const [busy, setBusy] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    await onPropose(recipient, amount);
    setBusy(false);
    setRecipient('');
    setAmount('');
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-3">
      <p className="font-montserratSemiBold text-primary-800">Propose a payment</p>
      <TextInput label="Recipient" value={recipient} onChange={setRecipient} placeholder="0x..." />
      <TextInput label="Amount (base units)" value={amount} onChange={setAmount} placeholder="1000000000000000000" />
      <Button type="submit" label={busy ? 'Proposing…' : 'Propose payment'} disabled={disabled || busy || !recipient || !amount} />
    </form>
  );
}

function ManageMembersSection({
  disabled,
  onAddMember,
  onRemoveMember,
  onChangeThreshold,
  onDisable,
}: {
  disabled: boolean;
  onAddMember: (username: string, role: GroupRole, newThreshold: number) => Promise<void>;
  onRemoveMember: (username: string, newThreshold: number) => Promise<void>;
  onChangeThreshold: (newThreshold: number) => Promise<void>;
  onDisable: () => Promise<void>;
}) {
  const [newUsername, setNewUsername] = useState('');
  const [newRole, setNewRole] = useState<GroupRole>('APPROVER');
  const [removeUsername, setRemoveUsername] = useState('');
  const [threshold, setThreshold] = useState('');
  const [busy, setBusy] = useState(false);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <p className="font-montserratSemiBold text-primary-800">Manage group</p>
      <div className="space-y-2">
        <TextInput label="Add member username" value={newUsername} onChange={setNewUsername} placeholder="username" />
        <select
          className="ring-1 ring-gray-200 focus:ring-primary-600 rounded-md w-full h-12 px-2"
          value={newRole}
          onChange={(e) => setNewRole(e.target.value as GroupRole)}
        >
          <option value="APPROVER">Approver</option>
          <option value="INITIATOR">Initiator</option>
          <option value="VIEW_ONLY">View only</option>
        </select>
        <TextInput label="New threshold after adding" value={threshold} onChange={setThreshold} placeholder="2" />
        <Button
          type="button"
          label="Propose add member"
          disabled={disabled || busy || !newUsername || !threshold}
          onClick={() => run(() => onAddMember(newUsername, newRole, Number(threshold)))}
        />
      </div>
      <div className="space-y-2">
        <TextInput label="Remove member username" value={removeUsername} onChange={setRemoveUsername} placeholder="username" />
        <Button
          type="button"
          label="Propose remove member"
          disabled={disabled || busy || !removeUsername || !threshold}
          onClick={() => run(() => onRemoveMember(removeUsername, Number(threshold)))}
        />
      </div>
      <div className="space-y-2">
        <Button
          type="button"
          label="Propose threshold change"
          disabled={disabled || busy || !threshold}
          onClick={() => run(() => onChangeThreshold(Number(threshold)))}
        />
      </div>
      <button
        type="button"
        className="text-trovored-primary text-sm underline"
        disabled={disabled || busy}
        onClick={() => run(onDisable)}
      >
        Propose disabling this group
      </button>
    </div>
  );
}
