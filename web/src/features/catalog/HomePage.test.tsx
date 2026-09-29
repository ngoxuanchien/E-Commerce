import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { renderApp } from '@/test/render';

test('shows categories and the newest active products first', async () => {
  renderApp('/');
  expect(await screen.findByRole('heading', { level: 1, name: 'Nhiều sạp hàng, một giỏ hàng.' })).toBeInTheDocument();
  const categories = await screen.findByRole('navigation', { name: 'Danh mục' });
  expect(await within(categories).findByRole('link', { name: 'Thời trang' })).toHaveAttribute('href', '/search?category=thoi-trang');
  const grid = await screen.findByRole('list', { name: 'Sản phẩm' });
  const names = within(grid).getAllByRole('heading', { level: 3 }).map((h) => h.textContent);
  expect(names[0]).toBe('Áo khoác gió unisex chống nước');
  expect(names).not.toContain('Áo polo vải cá sấu');
});
