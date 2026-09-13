// Cross-tab lock propagation (PLAN.md §5.3): locking in one tab must lock
// the wallet everywhere, not just where the idle timer or explicit lock
// button fired. BroadcastChannel is same-origin-scoped and needs no
// server round trip - exactly the right primitive for this.
const CHANNEL_NAME = 'wallet-web-lock';

let channel: BroadcastChannel | null = null;

function getChannel(): BroadcastChannel {
  if (!channel) {
    channel = new BroadcastChannel(CHANNEL_NAME);
  }
  return channel;
}

/** Tell every other open tab to lock. Call after this tab has already
 * locked its own Worker session. */
export function broadcastLock(): void {
  getChannel().postMessage({ type: 'LOCK_ALL' });
}

/** Subscribe to lock broadcasts from other tabs. Returns an unsubscribe
 * function. */
export function subscribeToLockBroadcasts(onLock: () => void): () => void {
  const ch = getChannel();
  const handler = (event: MessageEvent) => {
    if (event.data?.type === 'LOCK_ALL') onLock();
  };
  ch.addEventListener('message', handler);
  return () => ch.removeEventListener('message', handler);
}
