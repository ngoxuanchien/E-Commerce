import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { renderApp } from '@/test/render';

test("shows the shop header and only that shop's products", async () => {
  renderApp('/shops/gom-bat-trang');
  expect(await screen.findByRole('heading', { level: 1, name: 'Gốm Bát Tràng Cô Bình' })).toBeInTheDocument();
  expect(screen.getByText('Gốm men lam, men rạn làm thủ công tại làng Bát Tràng.')).toBeInTheDocument();
  const grid = await screen.findByRole('list', { name: 'Sản phẩm' });
  const names = within(grid).getAllByRole('heading', { level: 3 }).map((h) => h.textContent);
  expect(names).toHaveLength(4);
  expect(names).not.toContain('Áo thun cotton trơn cổ tròn');
});

test('unknown shops show a not-found message', async () => {
  renderApp('/shops/khong-ton-tai');
  expect(await screen.findByText('Shop không tồn tại')).toBeInTheDocument();
});
