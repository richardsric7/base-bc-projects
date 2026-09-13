import { useAppSelector } from '../store/hooks';

// The persistent online/offline/checking status chip (PLAN.md §6.4) -
// trovored (already the app's alert/negative color) for offline, a new
// positive/green token for online, a neutral pulse while checking.
export default function ConnectivityIndicator() {
  const { status, lastCheckedAt } = useAppSelector((s) => s.connectivity);

  const config = {
    online: { dot: 'bg-positive-primary', text: 'text-positive-primary', label: 'Online' },
    offline: { dot: 'bg-trovored-primary', text: 'text-trovored-primary', label: 'Offline' },
    checking: { dot: 'bg-primary-300 animate-pulse', text: 'text-primary-600', label: 'Checking…' },
  }[status];

  const title = lastCheckedAt
    ? `Last checked ${new Date(lastCheckedAt).toLocaleTimeString()}`
    : 'Not checked yet';

  return (
    <div className="flex items-center gap-2 font-montserratMedium text-sm" title={title}>
      <span className={`inline-block w-2.5 h-2.5 rounded-full ${config.dot}`} />
      <span className={config.text}>{config.label}</span>
    </div>
  );
}
