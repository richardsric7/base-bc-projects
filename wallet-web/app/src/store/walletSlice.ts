import { createSlice, type PayloadAction } from '@reduxjs/toolkit';
import type { WalletRole } from '../worker/protocol';

// PLAN.md §6.2's hard boundary: this slice holds ONLY public addresses
// and booleans (has-a-vault, is-unlocked) - never a mnemonic, password,
// or private key. Grep this file (and every other store/*Slice.ts) for
// "mnemonic"/"secretKey"/"privateKey" in code review; none should ever
// appear here.
export type { WalletRole };

export interface RoleState {
  address: string | null;
  hasVault: boolean;
  isUnlocked: boolean;
}

interface WalletState {
  signer: RoleState;
  // The primary wallet is a Safe with no private key of its own (PLAN.md
  // §13) - "vault"/"unlocked" here just means "we know its address," see
  // OnboardingWizard's finishWithPrimaryWallet.
  primary: RoleState;
}

const emptyRole: RoleState = { address: null, hasVault: false, isUnlocked: false };

const initialState: WalletState = {
  signer: { ...emptyRole },
  primary: { ...emptyRole },
};

const walletSlice = createSlice({
  name: 'wallet',
  initialState,
  reducers: {
    vaultCreated(state, action: PayloadAction<{ role: WalletRole; address: string }>) {
      state[action.payload.role] = { address: action.payload.address, hasVault: true, isUnlocked: true };
    },
    unlocked(state, action: PayloadAction<{ role: WalletRole; address: string }>) {
      state[action.payload.role].address = action.payload.address;
      state[action.payload.role].isUnlocked = true;
    },
    locked(state, action: PayloadAction<{ role: WalletRole }>) {
      state[action.payload.role].isUnlocked = false;
    },
    lockedAll(state) {
      state.signer.isUnlocked = false;
      state.primary.isUnlocked = false;
    },
    walletRemoved(state, action: PayloadAction<{ role: WalletRole }>) {
      state[action.payload.role] = { ...emptyRole };
    },
    hydrateKnownVaults(state, action: PayloadAction<{ signer: RoleState; primary: RoleState }>) {
      state.signer = action.payload.signer;
      state.primary = action.payload.primary;
    },
  },
});

export const { vaultCreated, unlocked, locked, lockedAll, walletRemoved, hydrateKnownVaults } = walletSlice.actions;
export default walletSlice.reducer;
