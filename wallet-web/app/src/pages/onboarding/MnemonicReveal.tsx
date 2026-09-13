import { useState } from 'react';
import Button from '../../components/Button';
import ButtonSecondary from '../../components/ButtonSecondary';
import { copyWithAutoClear } from '../../security/clipboard';

// Shows a freshly generated mnemonic exactly once, then requires the user
// to re-type it before continuing - a real check that they actually
// copied it down correctly, not just a "yes I saved it" checkbox.
type Props = {
  mnemonic: string;
  onConfirmed: () => void;
};

export default function MnemonicReveal({ mnemonic, onConfirmed }: Props) {
  const [stage, setStage] = useState<'reveal' | 'confirm'>('reveal');
  const [retyped, setRetyped] = useState('');
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    copyWithAutoClear(mnemonic).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 3000);
    });
  };

  const words = mnemonic.trim().split(/\s+/);

  const handleConfirm = () => {
    const normalize = (s: string) => s.trim().toLowerCase().replace(/\s+/g, ' ');
    if (normalize(retyped) !== normalize(mnemonic)) {
      setError('That does not match the phrase shown - please check and try again.');
      return;
    }
    onConfirmed();
  };

  if (stage === 'reveal') {
    return (
      <div className="space-y-4">
        <p className="text-primary-800 font-montserratSemiBold">Your recovery phrase</p>
        <p className="text-gray-600 text-sm">
          Write down these {words.length} words in order and store them somewhere safe. Anyone with this
          phrase can control this wallet - wallet-web will never ask for it again after this screen.
        </p>
        <div className="grid grid-cols-3 gap-2 bg-primary-100 rounded-md p-4 font-mono text-sm">
          {words.map((word, i) => (
            <div key={i} className="text-primary-800">
              <span className="text-primary-500 mr-1">{i + 1}.</span>
              {word}
            </div>
          ))}
        </div>
        <ButtonSecondary label={copied ? 'Copied!' : 'Copy to clipboard'} onClick={handleCopy} />
        <Button label="I've saved my recovery phrase" onClick={() => setStage('confirm')} />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <p className="text-primary-800 font-montserratSemiBold">Confirm your recovery phrase</p>
      <p className="text-gray-600 text-sm">Re-type the phrase you just saved to confirm it's correct.</p>
      <textarea
        className="ring-1 md:ring-2 ring-gray-200 focus-within:ring-primary-600 rounded-md w-full h-24 py-2 px-3 focus:outline-none font-mono text-sm text-gray-700"
        rows={4}
        value={retyped}
        onChange={(e) => setRetyped(e.target.value)}
        spellCheck={false}
        autoComplete="off"
      />
      {error && <p className="text-red-500 text-sm">{error}</p>}
      <Button label="Confirm" onClick={handleConfirm} />
    </div>
  );
}
