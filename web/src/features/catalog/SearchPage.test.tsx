import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { currentPath, renderApp } from '@/test/render';

const resultNames = async () => {
  const grid = await screen.findByRole('list', { name: 'Sản phẩm' });
  return within(grid).getAllByRole('heading', { level: 3 }).map((h) => h.textContent);
};

test('finds Vietnamese products when typed without diacritics', async () => {
  renderApp('/search?q=ao%20thun');
  expect(await screen.findByRole('heading', { level: 1, name: 'Kết quả cho “ao thun”' })).toBeInTheDocument();
  expect(await resultNames()).toEqual(expect.arrayContaining(['Áo thun cotton trơn cổ tròn', 'Áo thun in hình xe buýt Sài Gòn']));
});

test('applies category and price filters through the URL', async () => {
  const { user } = renderApp('/search');
  await user.click(await screen.findByText('Bộ lọc và sắp xếp'));
  await user.selectOptions(await screen.findByLabelText('Danh mục'), 'gom-su');
  await user.type(screen.getByLabelText('Đến (₫)'), '100000');
  await user.click(screen.getByRole('button', { name: 'Áp dụng' }));
  expect(currentPath()).toBe('/search?category=gom-su&max_price=100000');
  expect(await resultNames()).toEqual(['Bát ăn cơm men lam Bát Tràng']);
});

test('explains an empty result', async () => {
  renderApp('/search?q=khong%20co%20san%20pham%20nay');
  expect(await screen.findByText('Không tìm thấy sản phẩm phù hợp. Thử từ khoá khác hoặc bỏ bớt bộ lọc.')).toBeInTheDocument();
});
