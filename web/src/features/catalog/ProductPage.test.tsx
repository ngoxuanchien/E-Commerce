import { screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';

test('selects the first in-stock variant; sold-out variants are disabled and inactive ones hidden', async () => {
  renderApp(`/products/${SEED.products.busTee}`);
  expect(await screen.findByRole('heading', { level: 1, name: 'Áo thun in hình xe buýt Sài Gòn' })).toBeInTheDocument();
  expect(screen.getByRole('radio', { name: 'M' })).toBeChecked();
  expect(screen.getByRole('radio', { name: 'L (hết hàng)' })).toBeDisabled();
  expect(screen.getByText('Còn 12 sản phẩm')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Xem shop' })).toHaveAttribute('href', '/shops/ao-thun-sai-gon');
});

test('switching variant updates stock; inactive SKUs are not offered', async () => {
  const { user } = renderApp(`/products/${SEED.products.tee}`);
  await user.click(await screen.findByRole('radio', { name: 'L' }));
  expect(screen.getByText('Còn 10 sản phẩm')).toBeInTheDocument();
  expect(screen.queryByRole('radio', { name: /XL/ })).not.toBeInTheDocument();
});

test('anonymous users are sent to login and come back to the product', async () => {
  const { user } = renderApp(`/products/${SEED.products.bowl}`);
  await user.click(await screen.findByRole('button', { name: 'Thêm vào giỏ hàng' }));
  expect(currentPath()).toBe(`/login?next=%2Fproducts%2F${SEED.products.bowl}`);
});

test('adding twice accumulates the quantity and updates the header count', async () => {
  const { user } = renderApp(`/products/${SEED.products.bowl}`, { as: 'an@example.com' });
  await screen.findByRole('button', { name: 'An Nguyễn' });
  await screen.findByRole('heading', { level: 1, name: 'Bát ăn cơm men lam Bát Tràng' });
  await user.click(screen.getByRole('button', { name: 'Tăng số lượng' }));
  await user.click(screen.getByRole('button', { name: 'Thêm vào giỏ hàng' }));
  expect(await screen.findByText('Đã thêm vào giỏ hàng')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Thêm vào giỏ hàng' }));
  await waitFor(() => expect(getDb().state.carts[SEED.users.an]?.[SEED.skus.bowl11]).toBe(4));
  expect(await screen.findByRole('link', { name: 'Giỏ hàng, 4 sản phẩm' })).toBeInTheDocument();
});

test('sellers cannot add their own products', async () => {
  const { user } = renderApp(`/products/${SEED.products.bowl}`, { as: 'binh@example.com' });
  await screen.findByRole('button', { name: 'Bình Trần' });
  await screen.findByRole('heading', { level: 1, name: 'Bát ăn cơm men lam Bát Tràng' });
  await user.click(screen.getByRole('button', { name: 'Thêm vào giỏ hàng' }));
  expect(await screen.findByText('Không thể thêm sản phẩm này vào giỏ. Bạn không thể mua sản phẩm của chính shop mình.')).toBeInTheDocument();
});

test('draft products are visible to their owner only', async () => {
  renderApp(`/products/${SEED.products.polo}`);
  expect(await screen.findByText('Sản phẩm không tồn tại hoặc đã ngừng bán')).toBeInTheDocument();
});

test('the owner can preview a draft product', async () => {
  renderApp(`/products/${SEED.products.polo}`, { as: 'chi@example.com' });
  expect(await screen.findByRole('heading', { level: 1, name: 'Áo polo vải cá sấu' })).toBeInTheDocument();
});
