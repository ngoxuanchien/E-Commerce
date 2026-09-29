import { screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { currentPath, renderApp } from '@/test/render';

test('lists the buyer orders newest first and filters by status tab', async () => {
  const { user } = renderApp('/orders', { as: 'an@example.com' });
  await screen.findByRole('article', { name: 'Đơn hàng của Áo Thun Sài Gòn' });
  expect(screen.getAllByRole('article').map((c) => c.getAttribute('aria-label'))).toEqual([
    'Đơn hàng của Áo Thun Sài Gòn',
    'Đơn hàng của Gốm Bát Tràng Cô Bình',
  ]);
  await user.click(screen.getByRole('button', { name: 'Đã giao' }));
  expect(currentPath()).toBe('/orders?status=DELIVERED');
  expect(await screen.findByRole('button', { name: 'Đã giao', pressed: true })).toBeInTheDocument();
  await screen.findByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' });
  expect(screen.getAllByRole('article')).toHaveLength(1);
});

test('confirms a just-placed checkout', async () => {
  renderApp('/orders?placed=2', { as: 'an@example.com' });
  expect(await screen.findByText('Đặt hàng thành công. Đã tạo 2 đơn hàng, mỗi shop một đơn.')).toBeInTheDocument();
});

test('an empty tab invites the user to keep shopping', async () => {
  renderApp('/orders?status=SHIPPING', { as: 'an@example.com' });
  expect(await screen.findByText('Chưa có đơn hàng nào ở trạng thái này.')).toBeInTheDocument();
});
