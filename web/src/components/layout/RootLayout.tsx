import { Outlet } from 'react-router';
import { Toaster } from 'sonner';
import { AuthProvider } from '@/features/auth/AuthProvider';

export function RootLayout() {
  return (
    <AuthProvider>
      <Outlet />
      <Toaster position="top-center" richColors closeButton />
    </AuthProvider>
  );
}
