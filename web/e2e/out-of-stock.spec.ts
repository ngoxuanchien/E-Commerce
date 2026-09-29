import { expect, test } from '@playwright/test';
import { addToCart, checkout, login, search } from './helpers';

test('asking for more than the remaining stock sends the buyer back to the cart with the line flagged', async ({ page }) => {
  await login(page, 'an@example.com', 'An Nguyễn');
  await search(page, 'binh hoa');
  await addToCart(page, 'Bình hoa gốm men rạn cổ');

  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  const line = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: 'Bình hoa gốm men rạn cổ' }) });
  await line.getByRole('button', { name: 'Tăng số lượng' }).click();
  await expect(line.getByRole('textbox', { name: /Số lượng/ })).toHaveValue('2');

  await checkout(page);
  await expect(page).toHaveURL(/\/cart$/);
  await expect(page.getByText('Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.')).toBeVisible();
  await expect(line.getByText('Chỉ còn 1 sản phẩm')).toBeVisible();
});
