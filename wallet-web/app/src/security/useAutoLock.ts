import { useEffect } from 'react';
import { useIdleTimer } from 'react-idle-timer';
import { useAppDispatch } from '../store/hooks';
import { lockedAll } from '../store/walletSlice';
import { lockAllWallets, lockAllWalletsLocal } from '../core/walletCoreClient';
import { subscribeToLockBroadcasts } from './lockSync';

const IDLE_TIMEOUT_MS = 15 * 60 * 1000; // 15 minutes - PLAN.md §5.3

/**
 * Wires PLAN.md §5.3's two auto-lock paths: an idle timer (using
 * react-idle-timer, already a dependency of the original app - this time
 * actually wired to a wallet lock, fixing §1.2 finding 5's gap) and
 * cross-tab lock broadcasts. Mount once, only where a wallet can be
 * unlocked (AppLayout).
 */
export function useAutoLock(): void {
  const dispatch = useAppDispatch();

  const handleLock = () => {
    void lockAllWallets(); // also broadcasts to other tabs
    dispatch(lockedAll());
  };

  useIdleTimer({
    timeout: IDLE_TIMEOUT_MS,
    onIdle: handleLock,
    crossTab: false, // we handle cross-tab sync ourselves via BroadcastChannel
  });

  useEffect(() => {
    return subscribeToLockBroadcasts(() => {
      // Another tab locked and already broadcast - mirror it here without
      // re-broadcasting (lockAllWalletsLocal, not lockAllWallets), or two
      // open tabs would ping-pong lock messages back and forth forever.
      void lockAllWalletsLocal().catch(() => {});
      dispatch(lockedAll());
    });
  }, [dispatch]);
}
