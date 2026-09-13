import { useEffect, useState } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { useAppDispatch, useAppSelector } from './store/hooks';
import { hydrateKnownVaults } from './store/walletSlice';
import { hasVault, getKnownAddress } from './core/walletCoreClient';
import { startConnectivityMonitor } from './connectivity/connectivityMonitor';
import OnboardingWizard from './pages/onboarding/OnboardingWizard';
import Unlock from './pages/unlock/Unlock';
import Dashboard from './pages/dashboard/Dashboard';
import Send from './pages/send/Send';
import Swap from './pages/swap/Swap';
import Settings from './pages/settings/Settings';
import AppLayout from './layout/AppLayout';

function useVaultHydration() {
  const dispatch = useAppDispatch();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [signerHas, signerAddr, primaryHas, primaryAddr] = await Promise.all([
        hasVault('signer'),
        getKnownAddress('signer'),
        hasVault('primary'),
        getKnownAddress('primary'),
      ]);
      if (cancelled) return;
      dispatch(
        hydrateKnownVaults({
          signer: { address: signerAddr, hasVault: signerHas, isUnlocked: false },
          primary: { address: primaryAddr, hasVault: primaryHas, isUnlocked: false },
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
