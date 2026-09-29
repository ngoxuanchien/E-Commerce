import { createContext, useContext } from 'react';
import type { User } from '@/api/types';

export type AuthState = {
  status: 'loading' | 'ready';
  user: User | null;
  isSeller: boolean;
  login: (email: string, password: string) => Promise<User>;
  logout: () => Promise<void>;
  /** POST /api/auth/refresh, e.g. to pick up the seller role after opening a shop. */
  reloadSession: () => Promise<User | null>;
};

export const AuthContext = createContext<AuthState | null>(null);

export function useAuth(): AuthState {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error('useAuth must be used inside <AuthProvider>');
  return auth;
}
