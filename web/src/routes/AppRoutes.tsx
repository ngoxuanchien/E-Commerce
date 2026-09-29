import { Route, Routes } from 'react-router';
import { RequireAuth } from './guards';
import { RootLayout } from '@/components/layout/RootLayout';
import { StoreLayout } from '@/components/layout/StoreLayout';
import { NotFoundPage } from '@/components/states';
import { HomePage } from '@/features/catalog/HomePage';
import { ProductPage } from '@/features/catalog/ProductPage';
import { SearchPage } from '@/features/catalog/SearchPage';
import { ShopPage } from '@/features/shop/ShopPage';
import { CartPage } from '@/features/cart/CartPage';
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
          <Route path="shops/:slug" element={<ShopPage />} />
          <Route element={<RequireAuth />}>
            <Route path="cart" element={<CartPage />} />
            {/* authenticated routes (Tasks 14-18) */}
          </Route>
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
