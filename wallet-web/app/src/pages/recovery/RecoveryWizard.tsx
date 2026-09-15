import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import AuthLayout from '../../components/AuthLayout';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import TextInput from '../../components/TextInput';
import MnemonicReveal from '../onboarding/MnemonicReveal';
import { generateMnemonic, createVault, signRequestMessage } from '../../core/walletCoreClient';
import { getUserByUsername } from '../../api/usersApi';
import {
  listSecurityQuestions,
  requestAccountRecoveryOTP,
  recoverAccount,
  recoverWallet,
  buildRecoveryMessage,
  type SecurityQuestion,
} from '../../api/recoveryApi';
import { setCacheEntry, cacheKeys } from '../../cache/offlineCache';
import { useAppDispatch } from '../../store/hooks';
import { vaultCreated } from '../../store/walletSlice';
import { profileRegistered } from '../../store/authSlice';

type Branch = 'account' | 'wallet';
type Step = 'username' | 'choose-branch' | 'new-signer-reveal' | 'new-signer-password' | 'factors' | 'done';

// PLAN.md §13/wallet-backend PLAN.md §15: recovery is reachable without
// being logged in by definition (the whole point is having no working
// signer key) - a brand-new signer vault is generated right here, before
// any successful login, the one place in this app that happens.
export default function RecoveryWizard() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();

  const [step, setStep] = useState<Step>('username');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const [username, setUsername] = useState('');
  const [accountRecoveryAvailable, setAccountRecoveryAvailable] = useState(false);
  const [walletRecoveryAvailable, setWalletRecoveryAvailable] = useState(false);
  const [branch, setBranch] = useState<Branch>('account');

  const [newPhrase, setNewPhrase] = useState('');
  const [newAddress, setNewAddress] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');

  const [otp, setOtp] = useState('');
  const [otpRequested, setOtpRequested] = useState(false);
  const [questions, setQuestions] = useState<SecurityQuestion[]>([]);
  const [answers, setAnswers] = useState<Record<number, string>>({});

  const [resultAddress, setResultAddress] = useState('');
  const [resultTxHash, setResultTxHash] = useState('');

  useEffect(() => {
    if (step === 'factors' && questions.length === 0) {
      listSecurityQuestions()
        .then(setQuestions)
        .catch(() => setQuestions([]));
    }
  }, [step, questions.length]);

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

  const handleUsernameSubmit = () =>
    runStep(async () => {
      if (!username) throw new Error('Enter your username.');
      const user = await getUserByUsername(username);
      if (!user.accountRecoveryEnabled && !user.walletRecoveryEnabled) {
        throw new Error('This account has no recovery method enabled. Contact support for help.');
      }
      setAccountRecoveryAvailable(user.accountRecoveryEnabled);
      setWalletRecoveryAvailable(user.walletRecoveryEnabled);
      if (user.walletRecoveryEnabled && !user.accountRecoveryEnabled) {
        setBranch('wallet');
      } else {
        setBranch('account');
      }
      setStep(user.accountRecoveryEnabled && user.walletRecoveryEnabled ? 'choose-branch' : 'new-signer-reveal');
    });

  const handleGenerateNewSigner = () =>
    runStep(async () => {
      const mnemonic = await generateMnemonic(12);
      setNewPhrase(mnemonic);
      setStep('new-signer-reveal');
    });

  const handleNewSignerPasswordSubmit = () =>
    runStep(async () => {
      if (password.length < 8) throw new Error('Password must be at least 8 characters.');
      if (password !== confirmPassword) throw new Error('Passwords do not match.');
      const address = await createVault('signer', newPhrase, password);
      setNewPhrase('');
      setNewAddress(address);
      setStep('factors');
    });

  const handleRequestOtp = () =>
    runStep(async () => {
      await requestAccountRecoveryOTP(username);
      setOtpRequested(true);
    });

  const handleFinish = () =>
    runStep(async () => {
      if (!otp) throw new Error('Enter the OTP sent to your registered email.');
      const answerInputs = Object.entries(answers)
        .filter(([, value]) => value.trim() !== '')
        .map(([id, value]) => ({ securityQuestionId: Number(id), answer: value }));
      if (answerInputs.length === 0) throw new Error('Answer at least one of your security questions.');

      // A human-composed string message, never signHexDigest - see
      // recoveryApi.ts's own doc comments for why that distinction
      // matters (wallet-web/PLAN.md §15).
      const message = buildRecoveryMessage(username, newAddress);
      const signature = await signRequestMessage('signer', message);

      if (branch === 'account') {
        const log = await recoverAccount(username, newAddress, signature, otp, answerInputs);
        dispatch(vaultCreated({ role: 'signer', address: newAddress }));
        // Branch A's own design (wallet-backend PLAN.md §15.8): the
        // recovered account is a bare, undeployed EOA-as-wallet identity -
        // signer == wallet == newAddress, not a Safe.
        dispatch(vaultCreated({ role: 'primary', address: newAddress }));
        dispatch(profileRegistered({ username }));
        await setCacheEntry(cacheKeys.primaryWalletAddress(newAddress), newAddress);
        setResultAddress(log.newAddress);
      } else {
        const log = await recoverWallet(username, newAddress, signature, otp, answerInputs);
        dispatch(vaultCreated({ role: 'signer', address: newAddress }));
        dispatch(vaultCreated({ role: 'primary', address: log.walletAddress }));
        dispatch(profileRegistered({ username }));
        await setCacheEntry(cacheKeys.primaryWalletAddress(newAddress), log.walletAddress);
        setResultAddress(log.walletAddress);
        setResultTxHash(log.txHash);
      }
      setStep('done');
    });

  return (
    <AuthLayout title="Account recovery" subtitle="Regain access using your security answers and email OTP.">
      {error && <p className="text-red-500 text-sm">{error}</p>}

      {step === 'username' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">What's your username?</p>
          <TextInput label="Username" value={username} onChange={setUsername} />
          <Button label="Continue" onClick={handleUsernameSubmit} disabled={busy || !username} />
          <ButtonSecondary label="Back to unlock" onClick={() => navigate('/unlock')} />
        </div>
      )}

      {step === 'choose-branch' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Choose a recovery method</p>
          <p className="text-gray-600 text-sm">
            Wallet recovery keeps your existing wallet address, sub-wallets, and shared-access memberships.
            Account recovery is faster but starts you over with a fresh, empty wallet.
          </p>
          {walletRecoveryAvailable && (
            <ButtonSecondary
              label="Wallet recovery (keep everything)"
              onClick={() => {
                setBranch('wallet');
                setStep('new-signer-reveal');
              }}
            />
          )}
          {accountRecoveryAvailable && (
            <ButtonSecondary
              label="Account recovery (fresh start)"
              onClick={() => {
                setBranch('account');
                setStep('new-signer-reveal');
              }}
            />
          )}
        </div>
      )}

      {step === 'new-signer-reveal' && !newPhrase && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Generate a new signer key</p>
          <p className="text-gray-600 text-sm">
            This replaces the key you lost access to. Write down the new recovery phrase - you'll need it every
            time you use this wallet from now on.
          </p>
          <Button label="Generate new key" onClick={handleGenerateNewSigner} disabled={busy} />
        </div>
      )}

      {step === 'new-signer-reveal' && newPhrase && (
        <MnemonicReveal mnemonic={newPhrase} onConfirmed={() => setStep('new-signer-password')} />
      )}

      {step === 'new-signer-password' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Set a password</p>
          <p className="text-gray-600 text-sm">This encrypts your new signer wallet on this device.</p>
          <TextInput label="Password" type="password" value={password} onChange={setPassword} />
          <TextInput label="Confirm password" type="password" value={confirmPassword} onChange={setConfirmPassword} />
          <Button label="Continue" onClick={handleNewSignerPasswordSubmit} disabled={busy} />
        </div>
      )}

      {step === 'factors' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Verify it's you</p>
          <div className="space-y-2">
            <p className="text-gray-600 text-sm">
              We'll email a one-time code to your registered address.
            </p>
            <ButtonSecondary
              label={otpRequested ? 'Resend code' : 'Send code'}
              onClick={handleRequestOtp}
              disabled={busy}
            />
            <TextInput label="Code from email" value={otp} onChange={setOtp} />
          </div>
          <div className="space-y-2">
            <p className="text-gray-600 text-sm">Answer at least one security question you set up previously.</p>
            {questions.map((q) => (
              <TextInput
                key={q.id}
                label={q.question}
                value={answers[q.id] ?? ''}
                onChange={(v) => setAnswers((a) => ({ ...a, [q.id]: v }))}
              />
            ))}
          </div>
          <Button label="Recover account" onClick={handleFinish} disabled={busy || !otp} />
        </div>
      )}

      {step === 'done' && (
        <div className="space-y-4">
          <p className="text-primary-800 font-montserratSemiBold">Recovery complete</p>
          <p className="text-gray-600 text-sm">Your wallet address: {resultAddress}</p>
          {resultTxHash && <p className="text-gray-600 text-sm">Recovery transaction: {resultTxHash}</p>}
          <Button label="Go to dashboard" onClick={() => navigate('/dashboard')} />
        </div>
      )}
    </AuthLayout>
  );
}
