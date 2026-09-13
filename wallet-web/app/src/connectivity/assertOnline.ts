import { checkBackendReachable } from './connectivityMonitor';

export class OfflineError extends Error {
  constructor() {
    super('This action requires an internet connection.');
    this.name = 'OfflineError';
  }
}

/**
 * PLAN.md §6.4: the connectivity indicator is advisory for the UI, not
 * authoritative for the network call - every transaction build call
 * re-verifies reachability itself immediately beforehand, so a stale or
 * manipulated client-side status can at most cause a request that fails
 * cleanly, never a transaction attempted on assumptions never actually
 * verified. Called from paymentsApi/swapsApi's build functions.
 */
export async function assertOnline(): Promise<void> {
  const reachable = await checkBackendReachable();
  if (!reachable) {
    throw new OfflineError();
  }
}
