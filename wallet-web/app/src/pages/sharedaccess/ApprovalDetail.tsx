// The original's dashboard/sharedAccess/approvalDetails.tsx - view one
// PendingAction and either approve it (personal_sign over the exact
// digest getAction returns, per Send.tsx's own build->sign->submit
// pattern) or reject it with a reason.
import { useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { signHexDigest } from '../../core/walletCoreClient';
import { getAction, approveAction, rejectAction, type ActionDetail } from '../../api/sharedAccessApi';

export default function ApprovalDetail() {
  const { actionId } = useParams<{ actionId: string }>();
  const id = Number(actionId);
  const navigate = useNavigate();
  const isOnline = useIsOnline();
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);

  const [action, setAction] = useState<ActionDetail | null>(null);
  const [error, setError] = useState('');
  const [reason, setReason] = useState('');
  const [status, setStatus] = useState<'idle' | 'signing' | 'submitting' | 'done'>('idle');

  useEffect(() => {
    if (!primaryAddress) return;
    getAction(primaryAddress, id)
      .then(setAction)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [primaryAddress, id]);

  const handleApprove = async () => {
    if (!primaryAddress || !signerAddress || !action) return;
    setError('');
    try {
      setStatus('signing');
      const signature = await signHexDigest('signer', action.digestToSign);
      setStatus('submitting');
      const updated = await approveAction(primaryAddress, id, signature);
      setAction({ ...action, ...updated });
      setStatus('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus('idle');
    }
  };

  const handleReject = async () => {
    if (!primaryAddress || !reason) return;
    setError('');
    try {
      setStatus('submitting');
      const updated = await rejectAction(primaryAddress, id, reason);
      setAction((prev) => (prev ? { ...prev, ...updated } : prev));
      setStatus('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus('idle');
    }
  };

  if (error) return <p className="text-red-500 text-sm">{error}</p>;
  if (!action) return <p className="text-gray-600 text-sm">Loading…</p>;

  const busy = status === 'signing' || status === 'submitting';
  const terminal = action.status === 'EXECUTED' || action.status === 'REJECTED';

  return (
    <div className="max-w-lg space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Review action</h2>
        <button type="button" className="text-primary-700 text-sm underline" onClick={() => navigate('/shared-access/approvals')}>
          Back
        </button>
      </div>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to approve or reject.
        </p>
      )}
      <div className="bg-primary-100 rounded-lg p-4 space-y-1 text-sm">
        <p>Kind: {action.kind.replace('_', ' ')}</p>
        <p>Description: {action.description || '-'}</p>
        <p className="break-all">To: {action.to}</p>
        <p>Value: {action.value}</p>
        <p>Status: {action.status}</p>
        <p>Required approvals: {action.requiredApprovals}</p>
        {action.txHash && <p className="break-all">Tx hash: {action.txHash}</p>}
        {action.rejectionReason && <p className="text-trovored-primary">Rejected: {action.rejectionReason}</p>}
      </div>
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {!terminal && (
        <div className="space-y-4">
          <Button
            type="button"
            label={busy ? statusLabel(status) : 'Approve'}
            disabled={!isOnline || busy}
            onClick={handleApprove}
          />
          <div className="space-y-2">
            <TextInput label="Rejection reason" value={reason} onChange={setReason} placeholder="Why reject this?" />
            <button
              type="button"
              className="text-trovored-primary text-sm underline"
              disabled={!isOnline || busy || !reason}
              onClick={handleReject}
            >
              Reject
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function statusLabel(status: string): string {
  switch (status) {
    case 'signing':
      return 'Signing…';
    case 'submitting':
      return 'Submitting…';
    default:
      return 'Approve';
  }
}
