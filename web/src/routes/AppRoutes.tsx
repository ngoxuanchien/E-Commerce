import { Navigate, Route, Routes } from 'react-router';
import { RequireAuth, RequireSeller } from './guards';
import { RootLayout } from '@/components/layout/RootLayout';
import { StoreLayout } from '@/components/layout/StoreLayout';
import { NotFoundPage } from '@/components/states';
import { HomePage } from '@/features/catalog/HomePage';
import { ProductPage } from '@/features/catalog/ProductPage';
import { SearchPage } from '@/features/catalog/SearchPage';
import { ShopPage } from '@/features/shop/ShopPage';
import { CartPage } from '@/features/cart/CartPage';
import { CheckoutPage } from '@/features/checkout/CheckoutPage';
import { OrderDetailPage } from '@/features/orders/OrderDetailPage';
import { OrdersPage } from '@/features/orders/OrdersPage';
import { SellerLayout } from '@/components/layout/SellerLayout';
import { OpenShopPage } from '@/features/shop/OpenShopPage';
import { SellerShopPage } from '@/features/shop/SellerShopPage';
import { ProductFormPage } from '@/features/seller-products/ProductFormPage';
import { SellerProductsPage } from '@/features/seller-products/SellerProductsPage';
import { SellerOrdersPage } from '@/features/seller-orders/SellerOrdersPage';
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
            <Route path="checkout" element={<CheckoutPage />} />
            <Route path="orders" element={<OrdersPage />} />
            <Route path="orders/:id" element={<OrderDetailPage />} />
            <Route path="seller/open-shop" element={<OpenShopPage />} />
            <Route path="seller" element={<RequireSeller />}>
              <Route element={<SellerLayout />}>
                <Route index element={<Navigate to="products" replace />} />
                <Route path="shop" element={<SellerShopPage />} />
                <Route path="products" element={<SellerProductsPage />} />
                <Route path="products/new" element={<ProductFormPage />} />
                <Route path="products/:id" element={<ProductFormPage />} />
                <Route path="orders" element={<SellerOrdersPage />} />
              </Route>
            </Route>
          </Route>
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
