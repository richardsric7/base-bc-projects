import { configureStore } from '@reduxjs/toolkit';
import walletReducer from './walletSlice';
import authReducer from './authSlice';
import connectivityReducer from './connectivitySlice';

export const store = configureStore({
  reducer: {
    wallet: walletReducer,
    auth: authReducer,
    connectivity: connectivityReducer,
  },
});

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
