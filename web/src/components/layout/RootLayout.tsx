import type { ReactNode } from 'react';
import { Outlet } from 'react-router';
import { Toaster } from 'sonner';
import { PageSpinner } from '@/components/states';
import { useAuth } from '@/features/auth/auth-context';
import { AuthProvider } from '@/features/auth/AuthProvider';

/** Render routes only after the refresh-cookie session restore, so no page queries run anonymously first. */
function SessionGate({ children }: { children: ReactNode }) {
  const { status } = useAuth();
  return status === 'loading' ? <PageSpinner /> : children;
}

export function RootLayout() {
  return (
    <AuthProvider>
      <SessionGate>
        <Outlet />
      </SessionGate>
      <Toaster position="top-center" richColors closeButton />
    </AuthProvider>
  );
}
