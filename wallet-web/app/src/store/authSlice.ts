import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// The wallet-backend SIWE session. sessionToken is a short-lived bearer
// JWT (see wallet-backend's auth/services/services.go SessionTTL) - held
// in memory only (this Redux state is not persisted across reloads), a
// deliberately conservative choice consistent with this app's "nothing
// sensitive survives a reload implicitly" discipline (PLAN.md §5.3),
// even though a session token is a materially lower-stakes secret than a
// mnemonic. A reload requires signing in again.
interface AuthState {
  sessionToken: string | null;
  username: string | null;
}

const initialState: AuthState = {
  sessionToken: null,
  username: null,
};

const authSlice = createSlice({
  name: 'auth',
  initialState,
  reducers: {
    signedIn(state, action: PayloadAction<{ sessionToken: string; username: string | null }>) {
      state.sessionToken = action.payload.sessionToken;
      state.username = action.payload.username;
    },
    signedOut(state) {
      state.sessionToken = null;
      state.username = null;
    },
  },
});

export const { signedIn, signedOut } = authSlice.actions;
export default authSlice.reducer;
