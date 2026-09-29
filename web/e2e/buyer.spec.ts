import { expect, test } from '@playwright/test';
import { addToCart, checkout, expectNoHorizontalScroll, login, register, search } from './helpers';

test('buyer registers, searches without diacritics, buys from two shops and gets two orders', async ({ page }) => {
  const email = `minh.${Date.now()}.${test.info().project.name}@example.com`;
  await register(page, 'Minh Phạm', email);
  await login(page, email, 'Minh Phạm');

  await search(page, 'bat an com');
  await addToCart(page, 'Bát ăn cơm men lam Bát Tràng');
  await search(page, 'ao thun tron');
  await addToCart(page, 'Áo thun cotton trơn cổ tròn', 'M');

  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await expect(page.getByRole('region', { name: 'Shop Gốm Bát Tràng Cô Bình' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Shop Áo Thun Sài Gòn' })).toBeVisible();
  await expectNoHorizontalScroll(page);

  await checkout(page);
  await expect(page).toHaveURL(/\/orders\?placed=2$/);
  await expect(page.getByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' })).toBeVisible();
  await expect(page.getByRole('article', { name: 'Đơn hàng của Áo Thun Sài Gòn' })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
