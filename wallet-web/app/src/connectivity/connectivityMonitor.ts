import { BASE_URL } from '../api/httpClient';
import { statusChanged } from '../store/connectivitySlice';
import type { AppDispatch } from '../store';

// PLAN.md §6.4: navigator.onLine only reflects the network interface's
// state, not whether wallet-backend is actually reachable, so this layers
// an active health-ping against its existing GET / health endpoint
// (internal/components/root/controllers.go) on top of the passive
// browser signal.
const HEALTH_CHECK_TIMEOUT_MS = 5000;
const POLL_INTERVAL_MS = 15000;
const MAX_BACKOFF_MS = 60000;

export async function checkBackendReachable(): Promise<boolean> {
  if (!navigator.onLine) return false;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), HEALTH_CHECK_TIMEOUT_MS);
  try {
    const response = await fetch(`${BASE_URL}/`, { method: 'GET', signal: controller.signal });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
}

/**
 * Starts polling connectivity status into Redux. Returns a cleanup
 * function that stops the monitor - call once from the app root
 * (App.tsx) and clean up on unmount.
 */
export function startConnectivityMonitor(dispatch: AppDispatch): () => void {
  let backoff = POLL_INTERVAL_MS;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let stopped = false;
  let inFlight = false;

  async function runCheck() {
    if (inFlight) return;
    inFlight = true;
    dispatch(statusChanged('checking'));
    const reachable = await checkBackendReachable();
    dispatch(statusChanged(reachable ? 'online' : 'offline'));
    backoff = reachable ? POLL_INTERVAL_MS : Math.min(backoff * 2, MAX_BACKOFF_MS);
    inFlight = false;
    if (!stopped) {
      timer = setTimeout(runCheck, backoff);
    }
  }

  function recheckNow() {
    if (timer) clearTimeout(timer);
    backoff = POLL_INTERVAL_MS;
    void runCheck();
  }

  function handleVisibility() {
    if (document.visibilityState === 'visible') recheckNow();
  }

  window.addEventListener('online', recheckNow);
  window.addEventListener('offline', recheckNow);
  document.addEventListener('visibilitychange', handleVisibility);

  void runCheck();

  return () => {
    stopped = true;
    if (timer) clearTimeout(timer);
    window.removeEventListener('online', recheckNow);
    window.removeEventListener('offline', recheckNow);
    document.removeEventListener('visibilitychange', handleVisibility);
  };
}
