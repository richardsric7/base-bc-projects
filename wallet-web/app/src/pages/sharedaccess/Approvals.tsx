// The original's dashboard/sharedAccess/approvals.tsx - every group the
// caller belongs to, in one queue, rather than per-group (matching
// listPending's own "every action visible to this member across every
// group" shape).
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useAppSelector } from '../../store/hooks';
import { listPending, type PendingAction } from '../../api/sharedAccessApi';

export default function Approvals() {
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);
  const [actions, setActions] = useState<PendingAction[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!primaryAddress) return;
    listPending(primaryAddress)
      .then(setActions)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [primaryAddress]);

  return (
    <div className="max-w-lg space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Approvals</h2>
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {!actions && !error && <p className="text-gray-600 text-sm">Loading…</p>}
      {actions && actions.length === 0 && <p className="text-gray-600 text-sm">No pending actions.</p>}
      <ul className="space-y-2">
        {actions?.map((a) => (
          <li key={a.id} className="bg-primary-100 rounded-lg p-4 flex items-center justify-between">
            <div>
              <p className="font-montserratSemiBold text-primary-800">{a.kind.replace('_', ' ')}</p>
              <p className="text-gray-600 text-xs">{a.description || `Group #${a.groupId}`}</p>
              <p className="text-gray-500 text-xs">{a.status}</p>
            </div>
            <Link to={`/shared-access/approvals/${a.id}`} className="text-primary-700 text-sm underline">
              Review
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
