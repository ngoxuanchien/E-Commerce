import { screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';

const fillCart = (lines: Record<string, number>) => {
  getDb().state.carts[SEED.users.an] = lines;
};
const lineOf = (productName: string) => screen.getByRole('link', { name: productName }).closest('li')!;
const summary = () => screen.getByRole('complementary', { name: 'Tóm tắt' });

test('groups lines by shop and totals the selection', async () => {
  fillCart({ [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 });
  renderApp('/cart', { as: 'an@example.com' });
  expect(await screen.findByRole('region', { name: 'Shop Gốm Bát Tràng Cô Bình' })).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Shop Áo Thun Sài Gòn' })).toBeInTheDocument();
  expect(within(summary()).getByText('Tạm tính (3 sản phẩm)')).toBeInTheDocument();
  expect(within(summary()).getByText('239.000 ₫')).toBeInTheDocument();
});

test('deselecting a shop removes it from the subtotal and from checkout', async () => {
  fillCart({ [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 });
  const { user } = renderApp('/cart', { as: 'an@example.com' });
  await user.click(await screen.findByRole('checkbox', { name: 'Chọn tất cả sản phẩm của Áo Thun Sài Gòn' }));
  expect(within(summary()).getByText('90.000 ₫')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Mua hàng' }));
  expect(currentPath()).toBe(`/checkout?sku=${SEED.skus.bowl11}`);
});

test('quantity changes and removal go to the server', async () => {
  fillCart({ [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 });
  const { user } = renderApp('/cart', { as: 'an@example.com' });
  await screen.findByRole('link', { name: 'Bát ăn cơm men lam Bát Tràng' });
  await user.click(within(lineOf('Bát ăn cơm men lam Bát Tràng')).getByRole('button', { name: 'Tăng số lượng' }));
  await waitFor(() => expect(getDb().state.carts[SEED.users.an][SEED.skus.bowl11]).toBe(3));
  await user.click(screen.getByRole('button', { name: 'Xoá Áo thun cotton trơn cổ tròn (M)' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Shop Áo Thun Sài Gòn' })).not.toBeInTheDocument());
});

test('unavailable lines are flagged, excluded and their shop cannot be selected', async () => {
  fillCart({ [SEED.skus.bowl11]: 1, [SEED.skus.teeM]: 1 });
  getDb().updateProduct(SEED.users.chi, SEED.products.tee, { status: 'archived' });
  renderApp('/cart', { as: 'an@example.com' });
  expect(await screen.findByText('Sản phẩm không còn bán')).toBeInTheDocument();
  expect(screen.getByRole('checkbox', { name: 'Chọn tất cả sản phẩm của Áo Thun Sài Gòn' })).toBeDisabled();
  expect(within(summary()).getByText('45.000 ₫')).toBeInTheDocument();
});

test('shortages from a failed checkout are shown on the affected line', async () => {
  fillCart({ [SEED.skus.vase]: 3 });
  renderApp({ pathname: '/cart', state: { shortages: [{ sku_id: SEED.skus.vase, requested: 3, available: 1 }] } }, { as: 'an@example.com' });
  expect(await screen.findByText('Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.')).toBeInTheDocument();
  expect(within(lineOf('Bình hoa gốm men rạn cổ')).getByText('Chỉ còn 1 sản phẩm')).toBeInTheDocument();
});

test('an empty cart invites the user to keep shopping', async () => {
  renderApp('/cart', { as: 'an@example.com' });
  expect(await screen.findByText('Giỏ hàng đang trống')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Tiếp tục mua sắm' })).toHaveAttribute('href', '/');
});
