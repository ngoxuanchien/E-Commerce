import { Route, Routes } from 'react-router';
import { RootLayout } from '@/components/layout/RootLayout';
import { StoreLayout } from '@/components/layout/StoreLayout';
import { NotFoundPage } from '@/components/states';

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<RootLayout />}>
        <Route element={<StoreLayout />}>
          {/* public routes (Tasks 9-12) */}
          {/* <Route element={<RequireAuth />}> buyer + seller routes (Tasks 13-18) </Route> */}
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
