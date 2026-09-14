import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// PLAN.md §11: there is no server-side session anymore - every request
// is independently signed (api/httpClient.ts), so there's no token to
// hold here. This slice now only tracks the registered username once
// known, purely as a UI convenience (e.g. showing it in Settings)
// separate from the wallet's address, which lives in walletSlice.
interface AuthState {
  username: string | null;
}

const initialState: AuthState = {
  username: null,
};

const authSlice = createSlice({
  name: 'auth',
  initialState,
  reducers: {
    profileRegistered(state, action: PayloadAction<{ username: string | null }>) {
      state.username = action.payload.username;
    },
    signedOut(state) {
      state.username = null;
    },
  },
});

export const { profileRegistered, signedOut } = authSlice.actions;
export default authSlice.reducer;
