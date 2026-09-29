import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { openCheckout, placeOrderButton, signIn } from '../support/ui';

test('a buyer cancels a pending order and the stock returns within 5 s', async ({ page }) => {
  const run = runId();
  const shopName = `Kho Hàng ${run}`;
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(shopName, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Nồi đất kho cá ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  await signIn(page, buyer.account);
  await page.goto(`/products/${product.id}`);
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();
  await openCheckout(page);
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);
  expect(await seller.stock(product.id)).toBe(4);

  await page.getByRole('article', { name: `Đơn hàng của ${shopName}` }).getByRole('link', { name: 'Xem chi tiết' }).click();
  await page.getByRole('button', { name: 'Huỷ đơn hàng' }).click();
  await page.getByLabel('Lý do huỷ').fill('Đặt nhầm sản phẩm');
  await page.getByRole('button', { name: 'Xác nhận huỷ' }).click();
  await expect(page.getByText('Lý do huỷ: Đặt nhầm sản phẩm')).toBeVisible();

  await expect.poll(() => seller.stock(product.id), { timeout: 5_000, intervals: [200] }).toBe(5);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});
