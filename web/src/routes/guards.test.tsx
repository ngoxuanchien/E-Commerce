import { screen } from '@testing-library/react';
import { Route, Routes } from 'react-router';
import { expect, test } from 'vitest';
import { currentPath, renderWithProviders } from '@/test/render';
import { RequireAuth, RequireSeller } from './guards';

const tree = (
  <Routes>
    <Route path="/login" element={<p>Trang đăng nhập</p>} />
    <Route element={<RequireAuth />}>
      <Route path="/secret" element={<p>Nội dung riêng</p>} />
      <Route element={<RequireSeller />}>
        <Route path="/seller-only" element={<p>Kênh người bán</p>} />
      </Route>
    </Route>
  </Routes>
);

test('RequireAuth sends anonymous users to /login with the full next path', async () => {
  renderWithProviders(tree, { path: '/secret?tab=1' });
  expect(await screen.findByText('Trang đăng nhập')).toBeInTheDocument();
  expect(currentPath()).toBe('/login?next=%2Fsecret%3Ftab%3D1');
});

test('RequireAuth waits for the refresh-cookie session before deciding', async () => {
  renderWithProviders(tree, { path: '/secret', as: 'an@example.com' });
  expect(await screen.findByText('Nội dung riêng')).toBeInTheDocument();
});

test('RequireSeller shows the open-shop prompt to buyers', async () => {
  renderWithProviders(tree, { path: '/seller-only', as: 'an@example.com' });
  expect(await screen.findByRole('heading', { name: 'Bạn chưa có shop' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Mở shop của bạn' })).toHaveAttribute('href', '/seller/open-shop');
});

test('RequireSeller lets sellers through', async () => {
  renderWithProviders(tree, { path: '/seller-only', as: 'binh@example.com' });
  expect(await screen.findByText('Kênh người bán')).toBeInTheDocument();
});
