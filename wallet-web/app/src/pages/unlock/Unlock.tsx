import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import AuthLayout from '../../components/AuthLayout';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { unlockWallet } from '../../core/walletCoreClient';
import { unlocked } from '../../store/walletSlice';
import { useAppDispatch, useAppSelector } from '../../store/hooks';
import type { WalletRole } from '../../store/walletSlice';

// PLAN.md §0/§6.4: unlocking works fully offline - this page makes no
// network calls at all, only Worker calls (local Argon2id decrypt).
export default function Unlock() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const wallet = useAppSelector((s) => s.wallet);

  const rolesToUnlock: WalletRole[] = (['signer', 'primary'] as WalletRole[]).filter(
    (role) => wallet[role].hasVault && !wallet[role].isUnlocked,
  );

  const [passwords, setPasswords] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const handleUnlock = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      for (const role of rolesToUnlock) {
        const address = await unlockWallet(role, passwords[role] ?? '');
        dispatch(unlocked({ role, address }));
      }
      navigate('/dashboard');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthLayout title="Welcome back" subtitle="Unlock your wallet to continue.">
      <form onSubmit={handleUnlock} className="space-y-4">
        {rolesToUnlock.map((role) => (
          <TextInput
            key={role}
            label={role === 'signer' ? 'Signer password' : 'Primary wallet password'}
            type="password"
            value={passwords[role] ?? ''}
            onChange={(v) => setPasswords((p) => ({ ...p, [role]: v }))}
          />
        ))}
        {error && <p className="text-red-500 text-sm">{error}</p>}
        <Button type="submit" label="Unlock" disabled={busy || rolesToUnlock.length === 0} />
      </form>
      <p className="text-center text-sm">
        <Link to="/recovery" className="text-primary-700 font-montserratMedium">
          Lost your password or recovery phrase?
        </Link>
      </p>
    </AuthLayout>
  );
}
