// A textarea for entering/pasting a BIP-39 mnemonic phrase - the same
// pattern as trovo-wallet-monorepo/web's passphrase textarea
// (src/pages/importWallet/importWallet.tsx). Paste is intentionally left
// enabled (PLAN.md §7's threat-model table: blocking paste is more
// theater than defense and hurts recovery-phrase-restore UX).
type Props = {
  value: string;
  onChange: (value: string) => void;
  label: string;
  error?: string;
};

export default function MnemonicInput({ value, onChange, label, error = '' }: Props) {
  return (
    <div>
      <label className="text-primary-700 font-montserratMedium">{label}</label>
      <textarea
        className="mt-2 ring-1 md:ring-2 ring-gray-200 focus-within:ring-primary-600 rounded-md w-full h-24 py-2 px-3 focus:outline-none font-mono text-sm text-gray-700"
        placeholder="Enter your 12 or 24 word recovery phrase, separated by spaces"
        rows={4}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        autoComplete="off"
        autoCapitalize="off"
        autoCorrect="off"
      />
      {error && <p className="text-red-500 text-sm mt-1">{error}</p>}
    </div>
  );
}
