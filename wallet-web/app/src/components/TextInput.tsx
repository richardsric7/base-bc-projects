import { useState } from 'react';

// Ported styling from trovo-wallet-monorepo/web/src/components/textInput.tsx
// (PLAN.md §1.1) - same ring-based input convention, simplified (no
// leading/trailing icon images, which this app doesn't carry over).
type Props = {
  value: string;
  onChange: (value: string) => void;
  label?: string;
  placeholder?: string;
  type?: 'text' | 'password' | 'email';
  error?: string;
  autoComplete?: string;
};

export default function TextInput({
  value,
  onChange,
  label,
  placeholder = 'Enter value',
  type = 'text',
  error = '',
  autoComplete = 'off',
}: Props) {
  const [showPlainText, setShowPlainText] = useState(false);
  const isPassword = type === 'password';

  return (
    <div>
      {label && (
        <label className="text-primary-700 font-montserratMedium" htmlFor={label}>
          {label}
        </label>
      )}
      <div className="mt-2 ring-1 md:ring-2 ring-gray-200 focus-within:ring-primary-600 rounded-md w-full h-12 py-1 px-2 flex items-center">
        <input
          id={label}
          name={label ?? 'input'}
          className="h-full px-2 focus:outline-none w-full bg-inherit text-gray-700"
          placeholder={placeholder}
          type={isPassword && !showPlainText ? 'password' : 'text'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete={autoComplete}
        />
        {isPassword && (
          <button
            type="button"
            className="text-primary-700 text-sm font-montserratMedium px-1"
            aria-label={showPlainText ? 'Hide password' : 'Show password'}
            onClick={() => setShowPlainText(!showPlainText)}
          >
            {showPlainText ? 'Hide' : 'Show'}
          </button>
        )}
      </div>
      {error && <p className="text-red-500 text-sm mt-1">{error}</p>}
    </div>
  );
}
