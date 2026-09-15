import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import AuthLayout from '../../components/AuthLayout';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import TextInput from '../../components/TextInput';
import MnemonicInput from '../../components/MnemonicInput';
import MnemonicReveal from './MnemonicReveal';
import { generateMnemonic, validateMnemonic, createVault } from '../../core/walletCoreClient';
import { registerUser, getMyUser, deployPrimaryWallet } from '../../api/usersApi';
import { useAppDispatch } from '../../store/hooks';
import { vaultCreated } from '../../store/walletSlice';
import { profileRegistered } from '../../store/authSlice';
import { setCacheEntry, cacheKeys } from '../../cache/offlineCache';

type Step = 'choose-signer' | 'create-signer-reveal' | 'import-signer' | 'signer-password' | 'account' | 'done';

export default function OnboardingWizard() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();

  const [step, setStep] = useState<Step>('choose-signer');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // Transient - held only in this component's memory until the moment a
  // vault is created for it, then cleared. Never dispatched to Redux
  // (PLAN.md §6.2).
  const [signerPhrase, setSignerPhrase] = useState('');

  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [signerAddress, setSignerAddress] = useState('');
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');

  const runStep = async (fn: () => Promise<void>) => {
    setError('');
    setBusy(true);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleGenerateNew = () =>
    runStep(async () => {
      const mnemonic = await generateMnemonic(12);
      setSignerPhrase(mnemonic);
      setStep('create-signer-reveal');
    });

  const handleImportExisting = () => setStep('import-signer');

  const handleImportSignerSubmit = () =>
    runStep(async () => {
      const valid = await validateMnemonic(signerPhrase);
      if (!valid) {
        throw new Error('That does not look like a valid recovery phrase. Check the words and try again.');
      }
      setStep('signer-password');
    });

  // finishWithPrimaryWallet records the wallet-backend-computed primary
  // wallet address (a Safe smart-contract account, PLAN.md §13) as this
  // app's "primary" wallet - there is no local mnemonic/vault for it to
  // create: a Safe has no private key of its own, it's authorized
  // entirely by the signer's own signature (see api/httpClient.ts and
  // PLAN.md §17's fix in wallet-backend). Reusing the walletSlice
  // vaultCreated action here is a Redux-bookkeeping convenience, not a
  // literal claim that a vault was created - hasVault/isUnlocked being
  // true for "primary" just means "we know this wallet's address and it
  // needs no unlock step," which is trivially always true for a wallet
  // with no key to lock in the first place.
  const finishWithPrimaryWallet = (signerAddr: string, primaryWalletAddress: string) => {
    dispatch(vaultCreated({ role: 'primary', address: primaryWalletAddress }));
    // Persisted offline-safe (cache/offlineCache.ts) so App.tsx's reload
    // hydration can recognize this device as already onboarded without a
    // live round trip - see cacheKeys.primaryWalletAddress's doc comment.
    void setCacheEntry(cacheKeys.primaryWalletAddress(signerAddr), primaryWalletAddress);
    // Best-effort and non-blocking: a Safe that's never deployed has no
    // shared-access group and can't build/submit a payment or swap yet
    // (PLAN.md §17), so trigger deployment now rather than waiting on some
    // future funding/activation step. Idempotent and paid for by a
    // platform-operated key (services.DeployPrimaryWallet), so it's safe to
    // fire here and ignore a transient failure (e.g. no RPC connectivity) -
    // Send/Swap's own build calls are the real backstop and can retry it.
    void deployPrimaryWallet(signerAddr).catch(() => {});
    setStep('done');
  };

  const handleSignerPasswordSubmit = () =>
    runStep(async () => {
      if (password.length < 8) throw new Error('Password must be at least 8 characters.');
      if (password !== confirmPassword) throw new Error('Passwords do not match.');
      const address = await createVault('signer', signerPhrase, password);
      setSignerPhrase(''); // cleared the moment it's no longer needed
      setSignerAddress(address);
      dispatch(vaultCreated({ role: 'signer', address }));

      // No login round trip (PLAN.md §11) - every request is signed
      // independently, so a freshly-created (or re-imported) signer
      // vault can call the API immediately. If a profile already exists
      // for this address (re-importing a known wallet), skip straight to
      // done - wallet-backend already computed and returned this
      // signer's primary wallet address at registration time.
      try {
        const user = await getMyUser(address);
        dispatch(profileRegistered({ username: user.username }));
        finishWithPrimaryWallet(address, user.address);
      } catch {
        setStep('account');
      }
    });

  const handleAccountSubmit = () =>
    runStep(async () => {
      if (!username || !email) throw new Error('Username and email are required.');
      const user = await registerUser(signerAddress, username, email);
      dispatch(profileRegistered({ username: user.username }));
      finishWithPrimaryWallet(signerAddress, user.address);
    });

  return (
    <AuthLayout title="Welcome" subtitle="Set up your non-custodial Trovo Wallet.">
      {error && <p className="text-red-500 text-sm">{error}</p>}

      {step === 'choose-signer' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Get started</p>
          <Button label="Create a new wallet" onClick={handleGenerateNew} disabled={busy} />
          <ButtonSecondary label="Import an existing wallet" onClick={handleImportExisting} />
        </div>
      )}

      {step === 'create-signer-reveal' && (
        <MnemonicReveal mnemonic={signerPhrase} onConfirmed={() => setStep('signer-password')} />
      )}

      {step === 'import-signer' && (
        <div className="space-y-4">
          <MnemonicInput label="Signer recovery phrase" value={signerPhrase} onChange={setSignerPhrase} />
          <Button label="Continue" onClick={handleImportSignerSubmit} disabled={busy || !signerPhrase} />
        </div>
      )}

      {step === 'signer-password' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Set a password</p>
          <p className="text-gray-600 text-sm">
            This password encrypts your signer wallet on this device. It is never sent anywhere.
          </p>
          <TextInput label="Password" type="password" value={password} onChange={setPassword} />
          <TextInput label="Confirm password" type="password" value={confirmPassword} onChange={setConfirmPassword} />
          <Button label="Continue" onClick={handleSignerPasswordSubmit} disabled={busy} />
        </div>
      )}

      {step === 'account' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Create your profile</p>
          <TextInput label="Username" value={username} onChange={setUsername} />
          <TextInput label="Email" type="email" value={email} onChange={setEmail} />
          <Button label="Continue" onClick={handleAccountSubmit} disabled={busy} />
        </div>
      )}

      {step === 'done' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">You're all set</p>
          <Button label="Go to dashboard" onClick={() => navigate('/dashboard')} />
        </div>
      )}
    </AuthLayout>
  );
}
