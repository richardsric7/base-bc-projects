// A printable payment receipt - wallet-web/PLAN.md §19's closure of the
// original app's `pdfPages/sendAssetReceipt.tsx` gap. That page rendered
// a PDF with @react-pdf/renderer from a base64-encoded query param; this
// port shows the same fields (from/to/amount/tx hash+explorer link/date)
// as an ordinary page and leans on the browser's own print-to-PDF instead
// of adding a PDF-rendering dependency for one static document - a
// deliberate simplification, not a missing feature (the browser's "Save
// as PDF" print target produces the same PDF file the original handed
// back). Reached from Send.tsx's success state or a Dashboard history
// row via router state, not a URL param, since the record already lives
// in this app's own state at both call sites.
import { useLocation } from 'react-router-dom';
import { getNetworkConfig } from '../../config/network';
import type { PaymentHistoryRecord } from '../../api/paymentsApi';

function explorerUrl(chainId: number, txHash: string): string {
  const base = chainId === 8453 ? 'https://basescan.org/tx/' : 'https://sepolia.basescan.org/tx/';
  return base + txHash;
}

export default function Receipt() {
  const location = useLocation();
  const record = location.state as PaymentHistoryRecord | undefined;
  const { chainId } = getNetworkConfig();

  if (!record) {
    return <p className="text-gray-500 text-sm">No receipt to show - open this page from a completed payment.</p>;
  }

  return (
    <div className="max-w-md space-y-6 print:max-w-full">
      <div className="text-center">
        <h2 className="text-primary-800 font-matahariExtended text-xl">Payment receipt</h2>
        <p className="text-primary-700 text-sm">Generated {new Date().toLocaleString()}</p>
      </div>
      <div className="bg-primary-100 rounded-2xl px-5 py-6 space-y-4">
        <Row label="From" value={record.fromAddress} />
        <Row label="To" value={record.toAddress} />
        <Row label="Amount (base units)" value={record.amount} />
        {record.tokenAddress && <Row label="Token" value={record.tokenAddress} />}
        <Row
          label="Transaction hash"
          value={
            <a
              href={explorerUrl(chainId, record.txHash)}
              target="_blank"
              rel="noreferrer"
              className="text-primary-800 underline break-all"
            >
              {record.txHash}
            </a>
          }
        />
        <Row label="Date" value={new Date(record.createdAt).toLocaleString()} />
      </div>
      <button
        type="button"
        onClick={() => window.print()}
        className="print:hidden bg-primary-800 rounded-lg w-full text-white h-12 font-montserratMedium"
      >
        Print / Save as PDF
      </button>
    </div>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="border-b border-gray-200 pb-2 last:border-b-0 last:pb-0">
      <p className="text-primary-800 font-montserratSemiBold text-xs uppercase">{label}</p>
      <p className="text-primary-800 text-sm break-all">{value}</p>
    </div>
  );
}
