import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import { useAppDispatch, useAppSelector } from '../../store/hooks';
import { wipeWallet } from '../../core/walletCoreClient';
import { walletRemoved } from '../../store/walletSlice';
import { signedOut } from '../../store/authSlice';

export default function Settings() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const wallet = useAppSelector((s) => s.wallet);
  const [confirmingWipe, setConfirmingWipe] = useState(false);

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
