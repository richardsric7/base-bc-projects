import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { listCuratedTokens, getBalance, type CuratedToken } from '../../api/assetsApi';
import { getPaymentHistory, type PaymentHistoryRecord } from '../../api/paymentsApi';
import { setCacheEntry, getCacheEntry, cacheKeys } from '../../cache/offlineCache';
import CachedBanner from '../../components/CachedBanner';

interface Balance {
  label: string;
  contractAddress: string | null;
  amount: string;
}

export default function Dashboard() {
  const isOnline = useIsOnline();
  const primaryAddress = useAppSelector((s) => s.wallet.primary.address);

  const [balances, setBalances] = useState<Balance[]>([]);
  const [history, setHistory] = useState<PaymentHistoryRecord[]>([]);
  const [balancesAsOf, setBalancesAsOf] = useState<number | null>(null);
  const [historyAsOf, setHistoryAsOf] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!primaryAddress) return;
    let cancelled = false;

    (async () => {
      setLoading(true);

      let tokens: CuratedToken[] = [];
      if (isOnline) {
        try {
          tokens = await listCuratedTokens();
          await setCacheEntry(cacheKeys.curatedTokens(), tokens);
        } catch {
          // fall through to cache below
        }
      }
      if (tokens.length === 0) {
        const cached = await getCacheEntry<CuratedToken[]>(cacheKeys.curatedTokens());
        if (cached) tokens = cached.data;
      }
      if (cancelled) return;

      if (isOnline) {
        try {
          const native = await getBalance(primaryAddress);
          const tokenBalances = await Promise.all(
            tokens.map(async (t) => ({
              label: t.symbol,
              contractAddress: t.contractAddress,
              amount: await getBalance(primaryAddress, t.contractAddress),
            })),
          );
          const all: Balance[] = [{ label: 'ETH', contractAddress: null, amount: native }, ...tokenBalances];
          if (cancelled) return;
          setBalances(all);
          setBalancesAsOf(null);
          await setCacheEntry(cacheKeys.balances(primaryAddress), all);
        } catch {
          await loadCachedBalances();
        }
      } else {
        await loadCachedBalances();
      }

      if (isOnline) {
        try {
          const records = await getPaymentHistory(primaryAddress);
          if (cancelled) return;
          setHistory(records);
          setHistoryAsOf(null);
          await setCacheEntry(cacheKeys.paymentHistory(primaryAddress), records);
        } catch {
          await loadCachedHistory();
        }
      } else {
        await loadCachedHistory();
      }

      if (!cancelled) setLoading(false);

      async function loadCachedBalances() {
        const cached = await getCacheEntry<Balance[]>(cacheKeys.balances(primaryAddress!));
        if (cached && !cancelled) {
          setBalances(cached.data);
          setBalancesAsOf(cached.fetchedAt);
        }
      }
      async function loadCachedHistory() {
        const cached = await getCacheEntry<PaymentHistoryRecord[]>(cacheKeys.paymentHistory(primaryAddress!));
        if (cached && !cancelled) {
          setHistory(cached.data);
          setHistoryAsOf(cached.fetchedAt);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [primaryAddress, isOnline]);

  if (loading) {
    return <p className="text-primary-700">Loading…</p>;
  }

  return (
    <div className="space-y-8 max-w-2xl">
      <section className="space-y-3">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Balances</h2>
        {balancesAsOf && <CachedBanner asOf={balancesAsOf} />}
        <div className="divide-y divide-gray-100 rounded-md ring-1 ring-gray-100">
          {balances.map((b) => (
            <div key={b.contractAddress ?? 'native'} className="flex justify-between px-4 py-3">
              <span className="text-gray-700 font-montserratMedium">{b.label}</span>
              <span className="text-primary-800 font-mono">{b.amount}</span>
            </div>
          ))}
          {balances.length === 0 && <p className="px-4 py-3 text-gray-500 text-sm">No balances to show.</p>}
        </div>
      </section>

      <section className="space-y-3">
        <h2 className="text-primary-800 font-montserratSemiBold text-lg">Payment history</h2>
        {historyAsOf && <CachedBanner asOf={historyAsOf} />}
        <div className="divide-y divide-gray-100 rounded-md ring-1 ring-gray-100">
          {history.map((h) => (
            <div key={h.txHash} className="px-4 py-3 text-sm">
              <div className="flex justify-between">
                <span className="text-gray-700">{h.toAddress}</span>
                <span className="text-primary-800 font-mono">{h.amount}</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-gray-400 text-xs">{new Date(h.createdAt).toLocaleString()}</span>
                <Link to="/receipt" state={h} className="text-primary-700 text-xs underline">
                  Receipt
                </Link>
              </div>
            </div>
          ))}
          {history.length === 0 && <p className="px-4 py-3 text-gray-500 text-sm">No payments yet.</p>}
        </div>
      </section>
    </div>
  );
}
