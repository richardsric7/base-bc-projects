// Ported from trovo-wallet-monorepo/web/src/components/trovoBrand.tsx
// (PLAN.md §1.1).
type Props = {
  textColor?: string;
};

export default function Brand({ textColor = 'text-primary-800' }: Props) {
  return (
    <div className="flex items-center space-x-3 mt-5 mb-10 px-3 w-full">
      <img className="w-10" src="/images/trovoLogo.png" alt="Trovo logo" />
      <span className={`font-montserratMedium text-xl xl:text-2xl ${textColor}`}>Trovo Wallet</span>
    </div>
  );
}
