import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import { useAppDispatch, useAppSelector } from '../../store/hooks';
import { wipeWallet } from '../../core/walletCoreClient';
import { walletRemoved } from '../../store/walletSlice';
import { signedOut } from '../../store/authSlice';
import {
  getActiveNetwork,
  getNetworkConfig,
  isNetworkConfigured,
  switchActiveNetwork,
  type NetworkEnv,
} from '../../config/network';
import RecoverySettings from './RecoverySettings';

export default function Settings() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const wallet = useAppSelector((s) => s.wallet);
  const [confirmingWipe, setConfirmingWipe] = useState(false);
  const [pendingNetwork, setPendingNetwork] = useState<NetworkEnv | null>(null);
  const activeNetwork = getActiveNetwork();

  const handleWipe = async () => {
    await wipeWallet('signer');
    await wipeWallet('primary');
    dispatch(walletRemoved({ role: 'signer' }));
    dispatch(walletRemoved({ role: 'primary' }));
    dispatch(signedOut());
    navigate('/onboarding');
  };

  return (
    <div className="max-w-md space-y-8">
      <section className="space-y-2">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Wallets</h2>
        <p className="text-gray-600 text-sm">Signer: {wallet.signer.address ?? 'not set'}</p>
        <p className="text-gray-600 text-sm">Primary wallet: {wallet.primary.address ?? 'not set'}</p>
      </section>

      <section className="space-y-3">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Network</h2>
        <p className="text-gray-600 text-sm">
          Currently connected to <strong>{getNetworkConfig(activeNetwork).label}</strong>. Testnet and mainnet are
          separate wallet-backend deployments with separate accounts and balances - switching reloads the app and
          you'll need to sign in/register again on the other network if you haven't already.
        </p>
        <div className="flex gap-2">
          {(['testnet', 'mainnet'] as const).map((net) => {
            const isActive = net === activeNetwork;
            const configured = isNetworkConfigured(net);
            return (
              <button
                key={net}
                type="button"
                disabled={isActive || !configured}
                onClick={() => setPendingNetwork(net)}
                className={`rounded-lg border px-4 py-2 text-sm font-montserratMedium ${
                  isActive
                    ? 'border-primary-800 bg-primary-800 text-white'
                    : configured
                      ? 'border-gray-300 text-gray-700 hover:border-primary-800'
                      : 'cursor-not-allowed border-gray-200 text-gray-300'
                }`}
              >
                {getNetworkConfig(net).label}
                {!configured && !isActive ? ' (not configured)' : ''}
              </button>
            );
          })}
        </div>
        {pendingNetwork && (
          <div className="space-y-2 rounded-lg border border-gray-200 p-3">
            <p className="text-sm font-montserratMedium text-gray-700">
              Switch to {getNetworkConfig(pendingNetwork).label} and reload the app?
            </p>
            <div className="flex gap-2">
              <Button label="Switch and reload" onClick={() => switchActiveNetwork(pendingNetwork)} />
              <ButtonSecondary label="Cancel" onClick={() => setPendingNetwork(null)} />
            </div>
          </div>
        )}
      </section>

      <RecoverySettings />

      <section className="space-y-3">
        <h2 className="text-trovored-primary font-montserratSemiBold text-lg">Danger zone</h2>
        <p className="text-gray-600 text-sm">
          Removing your wallets deletes their encrypted vaults from this device. Make sure you have your
          recovery phrases saved before continuing - this cannot be undone.
        </p>
        {!confirmingWipe ? (
          <ButtonSecondary label="Remove wallets from this device" onClick={() => setConfirmingWipe(true)} />
        ) : (
          <div className="space-y-2">
            <p className="text-trovored-primary text-sm font-montserratMedium">Are you sure?</p>
            <Button label="Yes, remove wallets" onClick={handleWipe} additionalClasses="bg-trovored-primary" />
            <ButtonSecondary label="Cancel" onClick={() => setConfirmingWipe(false)} />
          </div>
        )}
      </section>
    </div>
  );
}
