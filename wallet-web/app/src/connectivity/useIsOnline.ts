import { useAppSelector } from '../store/hooks';

/** True only when connectivity status is confirmed "online" - PLAN.md
 * §6.4's enforcement point for gating transaction UI. */
export function useIsOnline(): boolean {
  return useAppSelector((s) => s.connectivity.status === 'online');
}
