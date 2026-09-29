import { screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';

test('opens a shop, refreshes the session for the seller role and enters the seller center', async () => {
  const { user } = renderApp('/seller/open-shop', { as: 'an@example.com' });
  await user.type(await screen.findByLabelText('Tên shop'), 'Gốm Chu Đậu Nhà An');
  expect(screen.getByLabelText('Địa chỉ shop')).toHaveValue('gom-chu-dau-nha-an');
  await user.click(screen.getByRole('button', { name: 'Mở shop' }));
  await waitFor(() => expect(currentPath()).toBe('/seller/products'));
  expect(getDb().state.users.find((u) => u.id === SEED.users.an)!.roles).toContain('seller');
  await user.click(screen.getByRole('button', { name: 'An Nguyễn' }));
  expect(screen.getByRole('link', { name: 'Kênh người bán' })).toBeInTheDocument();
});

test('a hand-edited slug is kept; reserved and taken slugs are rejected', async () => {
  const { user } = renderApp('/seller/open-shop', { as: 'an@example.com' });
  const slug = await screen.findByLabelText('Địa chỉ shop');
  await user.type(slug, 'admin');
  await user.type(screen.getByLabelText('Tên shop'), 'Shop Của An');
  expect(slug).toHaveValue('admin');
  await user.click(screen.getByRole('button', { name: 'Mở shop' }));
  expect(await screen.findByText('Địa chỉ này không được phép dùng')).toBeInTheDocument();
  await user.clear(slug);
  await user.type(slug, 'gom-bat-trang');
  await user.click(screen.getByRole('button', { name: 'Mở shop' }));
  expect(await screen.findByText('Địa chỉ shop này đã có người dùng.')).toBeInTheDocument();
});

test('sellers are sent straight to their products', async () => {
  renderApp('/seller/open-shop', { as: 'binh@example.com' });
  await waitFor(() => expect(currentPath()).toBe('/seller/products'));
});

test('/seller redirects sellers to /seller/products', async () => {
  renderApp('/seller', { as: 'binh@example.com' });
  await waitFor(() => expect(currentPath()).toBe('/seller/products'));
});

test('seller pages render inside the seller layout', async () => {
  renderApp('/seller/shop', { as: 'binh@example.com' });
  expect(await screen.findByRole('navigation', { name: 'Kênh người bán' })).toBeInTheDocument();
});
