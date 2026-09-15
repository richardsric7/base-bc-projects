// The original's own wallet directory - a wallet's Tag/Description/Alias
// exist so it can be shown and referred to in a friendly way (Tag/Alias)
// rather than by its raw address, and so a payment can be sent to it by
// alias alongside address/username/email (see paymentsApi.ts/Send.tsx and
// wallet-backend users.UserWallet's own doc comment). This page lists the
// caller's own directory and lets them edit each entry's Tag/Description/
// Alias in place.
import { useEffect, useState } from 'react';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { listMyWallets, updateWalletMetadata, type UserWallet } from '../../api/usersApi';

export default function MyWallets() {
  const isOnline = useIsOnline();
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);
  const [wallets, setWallets] = useState<UserWallet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const reload = () => {
    if (!signerAddress) return;
    setLoading(true);
    listMyWallets(signerAddress)
      .then(setWallets)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(reload, [signerAddress]);

  return (
    <div className="max-w-lg space-y-6">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">My Wallets</h2>
      <p className="text-gray-600 text-sm">
        Give each of your wallets a tag, description, and alias - the alias can be used to send a payment to this
        wallet, the same way an address, username, or email can.
      </p>
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {loading && <p className="text-gray-600 text-sm">Loading…</p>}
      <div className="space-y-4">
        {wallets.map((w) => (
          <WalletCard key={w.id} wallet={w} disabled={!isOnline} onSaved={reload} signerAddress={signerAddress} />
        ))}
      </div>
    </div>
  );
}

function WalletCard({
  wallet,
  disabled,
  onSaved,
  signerAddress,
}: {
  wallet: UserWallet;
  disabled: boolean;
  onSaved: () => void;
  signerAddress: string | null;
}) {
  const [tag, setTag] = useState(wallet.tag);
  const [description, setDescription] = useState(wallet.description);
  const [alias, setAlias] = useState(wallet.alias);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);

  const dirty = tag !== wallet.tag || description !== wallet.description || alias !== wallet.alias;

  const handleSave = async () => {
    if (!signerAddress) return;
    setBusy(true);
    setError('');
    setSaved(false);
    try {
      await updateWalletMetadata(signerAddress, wallet.address, { tag, description, alias });
      setSaved(true);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="bg-primary-100 rounded-lg p-4 space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-gray-600 text-xs break-all">{wallet.address}</p>
        {wallet.isPrimary && <span className="text-primary-800 text-xs font-montserratSemiBold">Primary</span>}
      </div>
      <TextInput label="Tag" value={tag} onChange={setTag} placeholder="e.g. savings" />
      <TextInput label="Description" value={description} onChange={setDescription} placeholder="What this wallet is for" />
      <TextInput label="Alias" value={alias} onChange={setAlias} placeholder="A friendly name others can send to" />
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {saved && !dirty && <p className="text-positive-primary text-sm">Saved.</p>}
      <Button type="button" label={busy ? 'Saving…' : 'Save'} disabled={disabled || busy || !dirty || !alias} onClick={handleSave} />
    </div>
  );
}
