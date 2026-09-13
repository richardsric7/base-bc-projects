import { useState } from 'react';
import Button from '../../components/Button';
import TextInput from '../../components/TextInput';
import { useAppSelector } from '../../store/hooks';
import { useIsOnline } from '../../connectivity/useIsOnline';
import { buildSwap, submitSwap } from '../../api/swapsApi';
import { signTransaction } from '../../core/walletCoreClient';

// wallet-backend's swaps component is a generic DEX router-call builder,
// not an abstracted "swap A for B" (PLAN.md §2: "the project configures
// which router it wants, this code doesn't pick for it"). This page
// exposes that same generic shape rather than hardcoding one DEX - a
// specific router's ABI/method is a deployment-time decision, not
// something wallet-web should bake in as a naive default.
export default function Swap() {
  const isOnline = useIsOnline();
  const sessionToken = useAppSelector((s) => s.auth.sessionToken);

  const [routerAddress, setRouterAddress] = useState('');
  const [routerAbi, setRouterAbi] = useState('');
  const [method, setMethod] = useState('');
  const [argsJson, setArgsJson] = useState('[]');
  const [valueWei, setValueWei] = useState('0');
  const [status, setStatus] = useState<'idle' | 'building' | 'signing' | 'submitting' | 'done'>('idle');
  const [error, setError] = useState('');
  const [txHash, setTxHash] = useState('');

  const handleSwap = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionToken) {
      setError('You must be signed in to swap.');
      return;
    }
    setError('');
    setTxHash('');
    try {
      const args = JSON.parse(argsJson || '[]');

      setStatus('building');
      const unsignedTx = await buildSwap(sessionToken, { routerAddress, routerAbi, method, args, valueWei });

      setStatus('signing');
      const signedTx = await signTransaction('primary', JSON.stringify(unsignedTx));

      setStatus('submitting');
      const { hash } = await submitSwap(sessionToken, signedTx);

      setTxHash(hash);
      setStatus('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus('idle');
    }
  };

  const busy = status !== 'idle' && status !== 'done';

  return (
    <div className="max-w-md space-y-4">
      <h2 className="text-primary-800 font-montserratSemiBold text-lg">Swap</h2>
      {!isOnline && (
        <p className="text-trovored-primary bg-trovored-light rounded-md px-3 py-2 text-sm">
          Connect to the internet to swap.
        </p>
      )}
      <form onSubmit={handleSwap} className="space-y-4">
        <TextInput label="Router address" value={routerAddress} onChange={setRouterAddress} placeholder="0x..." />
        <div>
          <label className="text-primary-700 font-montserratMedium">Router ABI (JSON)</label>
          <textarea
            className="mt-2 ring-1 md:ring-2 ring-gray-200 focus:ring-primary-600 rounded-md w-full h-24 py-2 px-3 font-mono text-xs"
            value={routerAbi}
            onChange={(e) => setRouterAbi(e.target.value)}
          />
        </div>
        <TextInput label="Method" value={method} onChange={setMethod} placeholder="swapExactTokensForTokens" />
        <div>
          <label className="text-primary-700 font-montserratMedium">Arguments (JSON array)</label>
          <textarea
            className="mt-2 ring-1 md:ring-2 ring-gray-200 focus:ring-primary-600 rounded-md w-full h-16 py-2 px-3 font-mono text-xs"
            value={argsJson}
            onChange={(e) => setArgsJson(e.target.value)}
          />
        </div>
        <TextInput label="Value (wei)" value={valueWei} onChange={setValueWei} />
        {error && <p className="text-red-500 text-sm">{error}</p>}
        {status === 'done' && <p className="text-positive-primary text-sm">Swap submitted. Tx hash: {txHash}</p>}
        <Button
          type="submit"
          label={busy ? 'Working…' : 'Swap'}
          disabled={!isOnline || busy || !routerAddress || !method}
        />
      </form>
    </div>
  );
}
