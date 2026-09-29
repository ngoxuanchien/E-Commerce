import { useQueryClient } from '@tanstack/react-query';
import { type ReactNode, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { api } from '@/api/client';
import { unwrap } from '@/api/errors';
import { clearSession, getSession, refreshSession, setAuthFailureHandler, setSession, subscribeSession } from '@/api/session';
import { AuthContext, type AuthState } from './auth-context';

export function AuthProvider({ children }: { children: ReactNode }) {
  const session = useSyncExternalStore(subscribeSession, getSession);
  const [status, setStatus] = useState<AuthState['status']>('loading');
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const locationRef = useRef(location);

  useEffect(() => {
    locationRef.current = location;
  }, [location]);

  // Restore the session from the HttpOnly refresh cookie (single-flight, so StrictMode's double effect is safe).
  useEffect(() => {
    let active = true;
    void refreshSession().finally(() => {
      if (active) setStatus('ready');
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    setAuthFailureHandler(() => {
      queryClient.clear();
      const { pathname, search } = locationRef.current;
      if (pathname === '/login') return;
      navigate(`/login?next=${encodeURIComponent(pathname + search)}`, { replace: true });
    });
  }, [navigate, queryClient]);

  const value = useMemo<AuthState>(
    () => ({
      status,
      user: session?.user ?? null,
      isSeller: session?.user.roles.includes('seller') ?? false,
      login: async (email, password) => {
        const token = await unwrap(api.POST('/api/auth/login', { body: { email, password } }));
        queryClient.clear();
        setSession(token);
        return token.user;
      },
      logout: async () => {
        await api.POST('/api/auth/logout').catch(() => undefined);
        clearSession();
        queryClient.clear();
        navigate('/');
      },
      reloadSession: async () => (await refreshSession())?.user ?? null,
    }),
    [status, session, queryClient, navigate],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
