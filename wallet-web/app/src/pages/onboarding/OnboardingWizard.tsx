import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import AuthLayout from '../../components/AuthLayout';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import TextInput from '../../components/TextInput';
import MnemonicInput from '../../components/MnemonicInput';
import MnemonicReveal from './MnemonicReveal';
import {
  generateMnemonic,
  validateMnemonic,
  previewAddress,
  createVault,
  signLinkPrimaryMessage,
} from '../../core/walletCoreClient';
import { signInWithSiwe } from '../../api/authFlow';
import { registerUser, linkPrimaryWallet, LinkPrimaryNotSupportedError, getUser } from '../../api/usersApi';
import { ApiError } from '../../api/httpClient';
import { useAppDispatch } from '../../store/hooks';
import { vaultCreated, setPrimarySameAsSigner } from '../../store/walletSlice';
import { signedIn } from '../../store/authSlice';

type Step =
  | 'choose-signer'
  | 'create-signer-reveal'
  | 'import-signer'
  | 'signer-password'
  | 'account'
  | 'primary-choice'
  | 'primary-password'
  | 'import-primary'
  | 'primary-password-separate'
  | 'done';

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
  const [primaryPhrase, setPrimaryPhrase] = useState('');

  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [signerAddress, setSignerAddress] = useState('');
  const [sessionToken, setSessionToken] = useState('');
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [primaryNote, setPrimaryNote] = useState('');

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

  const handleSignerPasswordSubmit = () =>
    runStep(async () => {
      if (password.length < 8) throw new Error('Password must be at least 8 characters.');
      if (password !== confirmPassword) throw new Error('Passwords do not match.');
      const address = await createVault('signer', signerPhrase, password);
      setSignerPhrase(''); // cleared the moment it's no longer needed
      setSignerAddress(address);
      dispatch(vaultCreated({ role: 'signer', address }));

      const { token } = await signInWithSiwe('signer', address);
      setSessionToken(token);
      dispatch(signedIn({ sessionToken: token, username: null }));

      // If a profile already exists for this address (re-importing a
      // known wallet), skip straight to primary-wallet setup.
      try {
        await getUser(address);
        setStep('primary-choice');
      } catch {
        setStep('account');
      }
    });

  const handleAccountSubmit = () =>
    runStep(async () => {
      if (!username || !email) throw new Error('Username and email are required.');
      const user = await registerUser(sessionToken, username, email);
      dispatch(signedIn({ sessionToken, username: user.username }));
      setStep('primary-choice');
    });

  const handleSameAsSigner = () =>
    runStep(async () => {
      setPassword('');
      setConfirmPassword('');
      setStep('primary-password');
    });

  // Mode 1 (PLAN.md §3): same mnemonic for both roles. The signer phrase
  // was already cleared once its own vault was created, so re-derive
  // nothing sensitive here - the user simply re-enters the same phrase,
  // which this step never had to retain.
  const [sameMnemonicReentry, setSameMnemonicReentry] = useState('');
  const handlePrimarySamePasswordSubmit = () =>
    runStep(async () => {
      const valid = await validateMnemonic(sameMnemonicReentry);
      if (!valid) throw new Error('That does not look like a valid recovery phrase.');
      const previewed = await previewAddress(sameMnemonicReentry);
      if (previewed.toLowerCase() !== signerAddress.toLowerCase()) {
        throw new Error('That phrase does not match your signer wallet address.');
      }
      if (password.length < 8) throw new Error('Password must be at least 8 characters.');
      if (password !== confirmPassword) throw new Error('Passwords do not match.');
      await createVault('primary', sameMnemonicReentry, password);
      setSameMnemonicReentry('');
      dispatch(vaultCreated({ role: 'primary', address: signerAddress }));
      dispatch(setPrimarySameAsSigner(true));
      setStep('done');
    });

  const handleImportPrimarySubmit = () =>
    runStep(async () => {
      const valid = await validateMnemonic(primaryPhrase);
      if (!valid) throw new Error('That does not look like a valid recovery phrase.');
      setStep('primary-password-separate');
    });

  const handlePrimarySeparatePasswordSubmit = () =>
    runStep(async () => {
      if (password.length < 8) throw new Error('Password must be at least 8 characters.');
      if (password !== confirmPassword) throw new Error('Passwords do not match.');
      const address = await createVault('primary', primaryPhrase, password);
      setPrimaryPhrase('');
      dispatch(vaultCreated({ role: 'primary', address }));
      dispatch(setPrimarySameAsSigner(false));

      try {
        const message = `Link ${address} as the primary wallet for signer ${signerAddress}.`;
        const signature = await signLinkPrimaryMessage(message);
        await linkPrimaryWallet(sessionToken, address, message, signature);
        setPrimaryNote('Primary wallet linked with wallet-backend.');
      } catch (err) {
        if (err instanceof LinkPrimaryNotSupportedError || (err instanceof ApiError && err.status === 404)) {
          setPrimaryNote(
            'Your primary wallet is stored locally, but this wallet-backend deployment does not yet support linking it server-side (PLAN.md §3). It will still be used to sign your payments.',
          );
        } else {
          throw err;
        }
      }
      setStep('done');
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

      {step === 'primary-choice' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Primary wallet</p>
          <p className="text-gray-600 text-sm">
            Your primary wallet holds your funds and payment history. You can use the same wallet you just
            signed in with, or import a different one (PLAN.md §3).
          </p>
          <Button label="Use the same wallet" onClick={handleSameAsSigner} disabled={busy} />
          <ButtonSecondary label="Import a different primary wallet" onClick={() => setStep('import-primary')} />
        </div>
      )}

      {step === 'primary-password' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Confirm your recovery phrase</p>
          <MnemonicInput label="Re-enter your recovery phrase" value={sameMnemonicReentry} onChange={setSameMnemonicReentry} />
          <TextInput label="Password" type="password" value={password} onChange={setPassword} />
          <TextInput label="Confirm password" type="password" value={confirmPassword} onChange={setConfirmPassword} />
          <Button label="Continue" onClick={handlePrimarySamePasswordSubmit} disabled={busy} />
        </div>
      )}

      {step === 'import-primary' && (
        <div className="space-y-4">
          <MnemonicInput label="Primary wallet recovery phrase" value={primaryPhrase} onChange={setPrimaryPhrase} />
          <Button label="Continue" onClick={handleImportPrimarySubmit} disabled={busy || !primaryPhrase} />
        </div>
      )}

      {step === 'primary-password-separate' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Set a password</p>
          <TextInput label="Password" type="password" value={password} onChange={setPassword} />
          <TextInput label="Confirm password" type="password" value={confirmPassword} onChange={setConfirmPassword} />
          <Button label="Continue" onClick={handlePrimarySeparatePasswordSubmit} disabled={busy} />
        </div>
      )}

      {step === 'done' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">You're all set</p>
          {primaryNote && <p className="text-gray-600 text-sm">{primaryNote}</p>}
          <Button label="Go to dashboard" onClick={() => navigate('/dashboard')} />
        </div>
      )}
    </AuthLayout>
  );
}
