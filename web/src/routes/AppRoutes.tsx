import { Route, Routes } from 'react-router';
import { RootLayout } from '@/components/layout/RootLayout';
import { StoreLayout } from '@/components/layout/StoreLayout';
import { NotFoundPage } from '@/components/states';
import { LoginPage } from '@/features/auth/LoginPage';
import { RegisterPage } from '@/features/auth/RegisterPage';

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<RootLayout />}>
        <Route element={<StoreLayout />}>
          <Route path="login" element={<LoginPage />} />
          <Route path="register" element={<RegisterPage />} />
          {/* public routes (Tasks 10-12) */}
          {/* <Route element={<RequireAuth />}> buyer + seller routes (Tasks 13-18) </Route> */}
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
