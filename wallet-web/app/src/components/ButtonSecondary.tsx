// Ported from trovo-wallet-monorepo/web/src/components/buttonSecondary.tsx
// (PLAN.md §1.1).
type Props = {
  onClick: () => void;
  label: string;
  disabled?: boolean;
};

export default function ButtonSecondary({ onClick, label, disabled = false }: Props) {
  const classes =
    'ring-1 ring-primary-800 rounded-md w-full text-primary-800 h-12 font-montserratMedium bg-white disabled:opacity-50 disabled:cursor-not-allowed';
  return (
    <button disabled={disabled} className={classes} type="button" onClick={onClick}>
      {label}
    </button>
  );
}
