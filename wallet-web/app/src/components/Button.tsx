// Ported styling from trovo-wallet-monorepo/web/src/components/button.tsx
// (PLAN.md §1.1) - same primary-800 rounded-lg button convention.
type Props = {
  label: string;
  disabled?: boolean;
  onClick?: () => void;
  type?: 'button' | 'submit' | 'reset';
  additionalClasses?: string;
};

export default function Button({
  onClick,
  label,
  disabled = false,
  type = 'button',
  additionalClasses = '',
}: Props) {
  const classes = `bg-primary-800 rounded-lg flex items-center justify-center gap-3 w-full text-white h-12 font-montserratMedium disabled:bg-primary-300 disabled:cursor-not-allowed ${additionalClasses}`;
  return (
    <button disabled={disabled} className={classes} type={type} onClick={onClick}>
      {label}
    </button>
  );
}
