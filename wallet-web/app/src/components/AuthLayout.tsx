import type { ReactNode } from 'react';
import Brand from './Brand';

// The split-screen auth-page convention ported from
// trovo-wallet-monorepo/web/src/pages/importWallet/importWallet.tsx
// (PLAN.md §1.1): a left brand panel on bg-primary-100, a right-hand form
// panel. The rafiki.png illustration in the left panel is that same
// page's own artwork (PLAN.md §14, wallet-web theme/artifact pass) -
// copied byte-for-byte from trovo-wallet-monorepo/web/public/images,
// never modified.
type Props = {
  title: string;
  subtitle?: string;
  children: ReactNode;
};

export default function AuthLayout({ title, subtitle, children }: Props) {
  return (
    <div className="flex min-h-screen items-stretch">
      <div className="hidden md:flex w-2/5 flex-col items-center bg-primary-100 p-3">
        <Brand />
        <p className="text-primary-800 font-matahariExtended text-center text-3xl xl:text-4xl font-bold mt-16">
          {title}
        </p>
        {subtitle && (
          <p className="text-primary-700 font-montserratRegular text-center mt-4 max-w-xs">{subtitle}</p>
        )}
        <img className="w-full max-w-sm mt-auto" src="/images/rafiki.png" alt="" />
      </div>
      <div className="w-full md:w-3/5 flex items-center justify-center py-12">
        <div className="w-full max-w-sm space-y-6 px-4">
          <div className="md:hidden">
            <Brand />
          </div>
          {children}
        </div>
      </div>
    </div>
  );
}
