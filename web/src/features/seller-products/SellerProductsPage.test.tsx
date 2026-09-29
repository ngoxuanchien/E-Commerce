import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { currentPath, renderApp } from '@/test/render';

test("lists the seller's products with status, price and stock", async () => {
  renderApp('/seller/products', { as: 'binh@example.com' });
  const table = await screen.findByRole('table', { name: 'Danh sách sản phẩm' });
  expect(within(table).getAllByRole('row')).toHaveLength(5); // header + 4 products
  const bowl = within(table).getByRole('row', { name: /Bát ăn cơm men lam Bát Tràng/ });
  expect(within(bowl).getByText('45.000 ₫')).toBeInTheDocument();
  expect(within(bowl).getByText('80')).toBeInTheDocument();
});

test('filters by status through the URL', async () => {
  const { user } = renderApp('/seller/products', { as: 'chi@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Nháp' }));
  expect(currentPath()).toBe('/seller/products?status=draft');
  expect(await screen.findByRole('row', { name: /Áo polo vải cá sấu/ })).toBeInTheDocument();
  expect(screen.getAllByRole('row')).toHaveLength(2);
});

test('archives a product after confirmation', async () => {
  const { user } = renderApp('/seller/products', { as: 'binh@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Lưu trữ Bình hoa gốm men rạn cổ' }));
  const dialog = await screen.findByRole('dialog', { name: 'Lưu trữ sản phẩm?' });
  await user.click(within(dialog).getByRole('button', { name: 'Lưu trữ' }));
  const row = await screen.findByRole('row', { name: /Bình hoa gốm men rạn cổ/ });
  expect((await within(row).findAllByText('Đã lưu trữ')).length).toBeGreaterThan(0); // badge (sm+) and inline label (mobile)
});
