import { useEffect, useState } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { useAppDispatch, useAppSelector } from './store/hooks';
import { hydrateKnownVaults } from './store/walletSlice';
import { hasVault, getKnownAddress } from './core/walletCoreClient';
import { getCacheEntry, setCacheEntry, cacheKeys } from './cache/offlineCache';
import { getMyUser } from './api/usersApi';
import { startConnectivityMonitor } from './connectivity/connectivityMonitor';
import OnboardingWizard from './pages/onboarding/OnboardingWizard';
import Unlock from './pages/unlock/Unlock';
import RecoveryWizard from './pages/recovery/RecoveryWizard';
import Dashboard from './pages/dashboard/Dashboard';
import Send from './pages/send/Send';
import Swap from './pages/swap/Swap';
import Settings from './pages/settings/Settings';
import FundWallet from './pages/fund/FundWallet';
import TokenizeHome from './pages/tokenize/TokenizeHome';
import AssetDetail from './pages/tokenize/AssetDetail';
import Receipt from './pages/receipt/Receipt';
import SharedAccessHome from './pages/sharedaccess/SharedAccessHome';
import GroupDetail from './pages/sharedaccess/GroupDetail';
import Approvals from './pages/sharedaccess/Approvals';
import ApprovalDetail from './pages/sharedaccess/ApprovalDetail';
import AppLayout from './layout/AppLayout';

// The primary wallet is a Safe with no private key of its own (PLAN.md
// §13/§17) - there is no local vault for it, so "do we know its address"
// comes from the offline cache (set at the end of onboarding) rather than
// hasVault('primary'). Falls back to a live GET /v1/users/me for a device
// that has a signer vault but never completed onboarding's cache write
// (e.g. it was cleared) - if that also fails (offline, never synced),
// primary stays unset and the app correctly falls back to onboarding.
async function resolvePrimaryWallet(signerAddr: string | null): Promise<string | null> {
  if (!signerAddr) return null;
  const cached = await getCacheEntry<string>(cacheKeys.primaryWalletAddress(signerAddr));
  if (cached) return cached.data;
  try {
    const user = await getMyUser(signerAddr);
    await setCacheEntry(cacheKeys.primaryWalletAddress(signerAddr), user.address);
    return user.address;
  } catch {
    return null;
  }
}

function useVaultHydration() {
  const dispatch = useAppDispatch();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [signerHas, signerAddr] = await Promise.all([hasVault('signer'), getKnownAddress('signer')]);
      const primaryAddr = await resolvePrimaryWallet(signerAddr);
      if (cancelled) return;
      dispatch(
        hydrateKnownVaults({
          signer: { address: signerAddr, hasVault: signerHas, isUnlocked: false },
          // No unlock step exists (or is needed) for a Safe with no key of
          // its own - see walletSlice.ts's RoleState doc comment - so
          // knowing its address makes it "unlocked" too, unlike signer.
          primary: { address: primaryAddr, hasVault: primaryAddr !== null, isUnlocked: primaryAddr !== null },
        }),
      );
      setReady(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [dispatch]);

  return ready;
}

export default function App() {
  const dispatch = useAppDispatch();
  const hydrated = useVaultHydration();
  const wallet = useAppSelector((s) => s.wallet);

  useEffect(() => {
    const stop = startConnectivityMonitor(dispatch);
    return stop;
  }, [dispatch]);

  if (!hydrated) {
    return <div className="min-h-screen flex items-center justify-center text-primary-700">Loading…</div>;
  }

  // Onboarding isn't "done" until BOTH roles have a vault - creating just
  // the signer vault must not redirect the wizard away before it reaches
  // primary-wallet setup (§3). This also correctly resumes onboarding
  // rather than dead-ending at Unlock if a user reloads mid-flow having
  // created a signer vault but not yet a primary one.
  const onboardingComplete = wallet.signer.hasVault && wallet.primary.hasVault;
  const isFullyUnlocked = wallet.signer.isUnlocked && wallet.primary.isUnlocked;

  return (
    <Routes>
      <Route path="/onboarding" element={onboardingComplete ? <Navigate to="/" replace /> : <OnboardingWizard />} />
      <Route path="/unlock" element={isFullyUnlocked ? <Navigate to="/dashboard" replace /> : <Unlock />} />
      {/* Reachable regardless of onboarding/unlock state - by definition, a
          user reaching for this has no working signer key to satisfy either
          of those checks (wallet-backend PLAN.md §15). */}
      <Route path="/recovery" element={<RecoveryWizard />} />
      <Route
        element={
          !onboardingComplete ? (
            <Navigate to="/onboarding" replace />
          ) : !isFullyUnlocked ? (
            <Navigate to="/unlock" replace />
          ) : (
            <AppLayout />
          )
        }
      >
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/send" element={<Send />} />
        <Route path="/swap" element={<Swap />} />
        <Route path="/fund" element={<FundWallet />} />
        <Route path="/tokenize" element={<TokenizeHome />} />
        <Route path="/tokenize/:assetId" element={<AssetDetail />} />
        <Route path="/receipt" element={<Receipt />} />
        <Route path="/shared-access" element={<SharedAccessHome />} />
        <Route path="/shared-access/groups/:groupId" element={<GroupDetail />} />
        <Route path="/shared-access/approvals" element={<Approvals />} />
        <Route path="/shared-access/approvals/:actionId" element={<ApprovalDetail />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route
        path="/"
        element={
          <Navigate to={!onboardingComplete ? '/onboarding' : !isFullyUnlocked ? '/unlock' : '/dashboard'} replace />
        }
      />
    </Routes>
  );
}
