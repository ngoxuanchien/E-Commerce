import { Route, Routes } from 'react-router';
import { RootLayout } from '@/components/layout/RootLayout';
import { StoreLayout } from '@/components/layout/StoreLayout';
import { NotFoundPage } from '@/components/states';
import { HomePage } from '@/features/catalog/HomePage';
import { ProductPage } from '@/features/catalog/ProductPage';
import { SearchPage } from '@/features/catalog/SearchPage';
import { LoginPage } from '@/features/auth/LoginPage';
import { RegisterPage } from '@/features/auth/RegisterPage';

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<RootLayout />}>
        <Route element={<StoreLayout />}>
          <Route path="login" element={<LoginPage />} />
          <Route path="register" element={<RegisterPage />} />
          <Route index element={<HomePage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="products/:id" element={<ProductPage />} />
          {/* public routes (Task 12) */}
          {/* <Route element={<RequireAuth />}> buyer + seller routes (Tasks 13-18) </Route> */}
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
