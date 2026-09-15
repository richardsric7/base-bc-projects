import { useEffect, useState } from 'react';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { getMyUser, type User } from '../../api/usersApi';
import { signHexDigest } from '../../core/walletCoreClient';
import {
  listSecurityQuestions,
  setSecurityAnswer,
  enableAccountRecovery,
  disableAccountRecovery,
  buildEnableWalletRecovery,
  confirmEnableWalletRecovery,
  buildDisableWalletRecovery,
  confirmDisableWalletRecovery,
  type SecurityQuestion,
} from '../../api/recoveryApi';

// wallet-backend PLAN.md §15: two independent recovery branches. This
// screen lets the signed-in owner set up the shared security-question
// factor and toggle each branch on/off - the actual recovery *execution*
// (for someone who's already locked out) lives at RecoveryWizard instead,
// reachable without being signed in at all.
export default function RecoverySettings() {
  const signerAddress = useAppSelector((s) => s.wallet.signer.address);
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);

  const [user, setUser] = useState<User | null>(null);
  const [questions, setQuestions] = useState<SecurityQuestion[]>([]);
  const [answers, setAnswers] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');

  const refresh = () => {
    if (!signerAddress) return;
    getMyUser(signerAddress)
      .then(setUser)
      .catch(() => setUser(null));
  };

  useEffect(refresh, [signerAddress]);
  useEffect(() => {
    listSecurityQuestions()
      .then(setQuestions)
      .catch(() => setQuestions([]));
  }, []);

  const runAction = async (fn: () => Promise<void>) => {
    setError('');
    setStatus('');
    setBusy(true);
    try {
      await fn();
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleSaveAnswers = () =>
    runAction(async () => {
      if (!primaryAddress) throw new Error('Primary wallet address is not known yet.');
      const entries = Object.entries(answers).filter(([, v]) => v.trim() !== '');
      if (entries.length === 0) throw new Error('Answer at least one security question first.');
      for (const [id, answer] of entries) {
        await setSecurityAnswer(primaryAddress, Number(id), answer);
      }
      setAnswers({});
      setStatus('Security answers saved.');
    });

  const handleToggleAccountRecovery = (enable: boolean) =>
    runAction(async () => {
      if (!primaryAddress) throw new Error('Primary wallet address is not known yet.');
      if (enable) await enableAccountRecovery(primaryAddress);
      else await disableAccountRecovery(primaryAddress);
      setStatus(enable ? 'Account recovery enabled.' : 'Account recovery disabled.');
    });

  const handleEnableWalletRecovery = () =>
    runAction(async () => {
      if (!primaryAddress || !signerAddress) throw new Error('Wallet is not fully set up yet.');
      const challenge = await buildEnableWalletRecovery(primaryAddress);
      if (challenge.feeTx) {
        throw new Error(
          'This deployment charges a one-time fee to enable wallet recovery, and broadcasting that ' +
            'fee transaction yourself is not yet supported in this app. Contact support to enable it.',
        );
      }
      const addOwnerSignature = await signHexDigest('signer', challenge.addOwnerSafeTxHash);
      const setGuardSignature = await signHexDigest('signer', challenge.setGuardSafeTxHash);
      await confirmEnableWalletRecovery(primaryAddress, '', addOwnerSignature, setGuardSignature);
      setStatus('Wallet recovery enabled.');
    });

  const handleDisableWalletRecovery = () =>
    runAction(async () => {
      if (!primaryAddress) throw new Error('Primary wallet address is not known yet.');
      const challenge = await buildDisableWalletRecovery(primaryAddress);
      const removeOwnerSignature = await signHexDigest('signer', challenge.removeOwnerSafeTxHash);
      const clearGuardSignature = await signHexDigest('signer', challenge.clearGuardSafeTxHash);
      await confirmDisableWalletRecovery(primaryAddress, removeOwnerSignature, clearGuardSignature);
      setStatus('Wallet recovery disabled.');
    });

  return (
    <section className="space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Recovery</h2>
      {error && <p className="text-red-500 text-sm">{error}</p>}
      {status && <p className="text-positive-primary text-sm">{status}</p>}

      <div className="space-y-2">
        <p className="text-gray-700 font-montserratMedium text-sm">Security questions</p>
        <p className="text-gray-600 text-sm">
          Both recovery methods below need at least one answered security question, plus an emailed one-time
          code, to prove it's really you.
        </p>
        {questions.map((q) => (
          <TextInput
            key={q.id}
            label={q.question}
            value={answers[q.id] ?? ''}
            onChange={(v) => setAnswers((a) => ({ ...a, [q.id]: v }))}
          />
        ))}
        <ButtonSecondary label="Save answers" onClick={handleSaveAnswers} disabled={busy} />
      </div>

      <div className="space-y-2 rounded-lg border border-gray-200 p-3">
        <p className="text-gray-700 font-montserratMedium text-sm">
          Account recovery {user?.accountRecoveryEnabled ? '(enabled)' : '(disabled)'}
        </p>
        <p className="text-gray-600 text-sm">
          Free. If you lose your key, your account gets a fresh wallet address - anything at the old address is
          abandoned.
        </p>
        {user?.accountRecoveryEnabled ? (
          <ButtonSecondary label="Disable" onClick={() => handleToggleAccountRecovery(false)} disabled={busy} />
        ) : (
          <Button label="Enable" onClick={() => handleToggleAccountRecovery(true)} disabled={busy} />
        )}
      </div>

      <div className="space-y-2 rounded-lg border border-gray-200 p-3">
        <p className="text-gray-700 font-montserratMedium text-sm">
          Wallet recovery {user?.walletRecoveryEnabled ? '(enabled)' : '(disabled)'}
        </p>
        <p className="text-gray-600 text-sm">
          Keeps your existing wallet address, sub-wallets, and shared-access memberships if you lose your key -
          a recovery service is added as a second owner of your wallet, restricted to swapping owners only.
        </p>
        {!user?.primaryWalletDeployed ? (
          <p className="text-gray-500 text-xs">Your primary wallet must be deployed on-chain first.</p>
        ) : user?.walletRecoveryEnabled ? (
          <ButtonSecondary label="Disable" onClick={handleDisableWalletRecovery} disabled={busy} />
        ) : (
          <Button label="Enable" onClick={handleEnableWalletRecovery} disabled={busy} />
        )}
      </div>
    </section>
  );
}
