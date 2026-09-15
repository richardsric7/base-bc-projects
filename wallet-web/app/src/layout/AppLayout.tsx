import { Outlet, NavLink } from 'react-router-dom';
import Brand from '../components/Brand';
import ConnectivityIndicator from '../components/ConnectivityIndicator';
import { useAppDispatch } from '../store/hooks';
import { lockAllWallets } from '../core/walletCoreClient';
import { lockedAll } from '../store/walletSlice';
import { useAutoLock } from '../security/useAutoLock';

const navLinkClasses = ({ isActive }: { isActive: boolean }) =>
  `px-3 py-2 rounded-md font-montserratMedium text-sm ${
    isActive ? 'bg-primary-100 text-primary-800' : 'text-primary-700 hover:bg-primary-100'
  }`;

export default function AppLayout() {
  const dispatch = useAppDispatch();
  useAutoLock(); // PLAN.md §5.3: idle timeout + cross-tab lock sync

  const handleLock = async () => {
    await lockAllWallets();
    dispatch(lockedAll());
  };

  return (
    <div className="min-h-screen bg-white">
      <header className="flex items-center justify-between border-b border-gray-100 px-4 py-2">
        <Brand />
        <nav className="flex items-center gap-2">
          <NavLink to="/dashboard" className={navLinkClasses}>
            Dashboard
          </NavLink>
          <NavLink to="/send" className={navLinkClasses}>
            Send
          </NavLink>
          <NavLink to="/swap" className={navLinkClasses}>
            Swap
          </NavLink>
          <NavLink to="/fund" className={navLinkClasses}>
            Fund
          </NavLink>
          <NavLink to="/tokenize" className={navLinkClasses}>
            Tokenize
          </NavLink>
          <NavLink to="/settings" className={navLinkClasses}>
            Settings
          </NavLink>
        </nav>
        <div className="flex items-center gap-4">
          <ConnectivityIndicator />
          <button
            className="text-sm text-trovored-primary font-montserratMedium"
            onClick={handleLock}
            type="button"
          >
            Lock
          </button>
        </div>
      </header>
      <main className="p-6">
        <Outlet />
      </main>
    </div>
  );
}
