import { expect, test, type Page } from '@playwright/test';
import { addToCart, checkout, login, register, search } from './helpers';

const BOWL_SHOP = 'Đơn hàng của Gốm Bát Tràng Cô Bình';
const TEE_SHOP = 'Đơn hàng của Áo Thun Sài Gòn';

async function buyerWithTwoShopCart(page: Page, tag: string) {
  const email = `pay.${tag}.${Date.now()}.${test.info().project.name}@example.com`;
  await register(page, 'Minh Phạm', email);
  await login(page, email, 'Minh Phạm');
  await search(page, 'bat an com');
  await addToCart(page, 'Bát ăn cơm men lam Bát Tràng');
  await search(page, 'ao thun tron');
  await addToCart(page, 'Áo thun cotton trơn cổ tròn', 'M');
}

test('online checkout from two shops pays through the mock gateway', async ({ page }) => {
  await buyerWithTwoShopCart(page, 'ok');
  await checkout(page, 'VNPAY');
  await expect(page).toHaveURL(/\/__mock\/pay\//);
  await page.getByRole('button', { name: 'Thành công' }).click();

  await expect(page.getByTestId('payment-result')).toHaveAttribute('data-status', 'SUCCEEDED');
  await expect(page.getByRole('heading', { name: 'Thanh toán thành công' })).toBeVisible();
  await page.getByRole('link', { name: 'Xem đơn mua' }).first().click();
  await expect(page.getByRole('article', { name: TEE_SHOP })).toContainText('Đã thanh toán');
  await expect(page.getByRole('article', { name: BOWL_SHOP })).toContainText('Đã thanh toán');
});

test('a failed payment can be retried with pay again', async ({ page }) => {
  await buyerWithTwoShopCart(page, 'retry');
  await checkout(page, 'VNPAY');
  await page.getByRole('button', { name: 'Thất bại' }).click();
  await expect(page.getByRole('heading', { name: 'Thanh toán không thành công' })).toBeVisible();

  await page.getByTestId('pay-again').click();
  await expect(page).toHaveURL(/\/__mock\/pay\//);
  await page.getByRole('button', { name: 'Thành công' }).click();
  await expect(page.getByRole('heading', { name: 'Thanh toán thành công' })).toBeVisible();
});

test('an order awaiting payment can be cancelled', async ({ page }) => {
  await buyerWithTwoShopCart(page, 'cancel');
  await checkout(page, 'VNPAY');
  await expect(page).toHaveURL(/\/__mock\/pay\//);

  await page.goto('/orders?status=AWAITING_PAYMENT');
  await page.getByRole('article').first().getByRole('link', { name: 'Xem chi tiết' }).click();
  await page.getByRole('button', { name: 'Huỷ đơn hàng' }).click();
  await page.getByLabel('Lý do huỷ').fill('Đổi ý');
  await page.getByRole('button', { name: 'Xác nhận huỷ' }).click();
  await expect(page.getByRole('dialog')).toBeHidden();

  await page.goto('/orders?status=CANCELLED');
  await expect(page.getByRole('article', { name: BOWL_SHOP })).toBeVisible();
  await expect(page.getByRole('article', { name: TEE_SHOP })).toBeVisible();
});
