import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// PLAN.md §6.4: connectivity is not secret, so it lives alongside the
// rest of the app's non-sensitive UI state without conflict.
export type ConnectivityStatus = 'online' | 'offline' | 'checking';

interface ConnectivityState {
  status: ConnectivityStatus;
  lastCheckedAt: number | null;
  lastOnlineAt: number | null;
}

const initialState: ConnectivityState = {
  status: 'checking',
  lastCheckedAt: null,
  lastOnlineAt: null,
};

const connectivitySlice = createSlice({
  name: 'connectivity',
  initialState,
  reducers: {
    statusChanged(state, action: PayloadAction<ConnectivityStatus>) {
      state.status = action.payload;
      state.lastCheckedAt = Date.now();
      if (action.payload === 'online') {
        state.lastOnlineAt = state.lastCheckedAt;
      }
    },
  },
});

export const { statusChanged } = connectivitySlice.actions;
export default connectivitySlice.reducer;
