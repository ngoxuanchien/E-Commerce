import { type Page, expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { openCheckout, placeOrderButton, signIn } from '../support/ui';

async function setup(page: Page) {
  const run = runId();
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(`Tạp Hóa ${run}`, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Mứt gừng Huế ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);
  await buyer.setCartItem(product.skus[0].id, 1);
  await signIn(page, buyer.account);
  await openCheckout(page);

  const keys: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith('/api/checkout')) keys.push(r.headers()['idempotency-key']);
  });
  return { seller, buyer, product, keys };
}

/** The server commits the first checkout, but the browser never sees the answer. */
async function loseFirstCheckoutResponse(page: Page) {
  let first = true;
  await page.route('**/api/checkout', async (route) => {
    if (!first) return route.continue();
    first = false;
    await route.fetch();
    await route.abort('connectionreset');
  });
}

test('double-clicking place order creates one set of orders', async ({ page }) => {
  const { seller, buyer, product, keys } = await setup(page);

  await placeOrderButton(page).dblclick();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);

  expect(new Set(keys).size).toBe(1);
  expect(await buyer.orders()).toHaveLength(1);
  expect(await seller.stock(product.id)).toBe(4);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});

test('a checkout whose response is lost is retried with the same key and not duplicated', async ({ page }) => {
  const { seller, buyer, product, keys } = await setup(page);

  await loseFirstCheckoutResponse(page);

  await placeOrderButton(page).click();
  // The retryable-error toast re-runs the same mutation, so the retry carries the same key.
  await page.getByRole('button', { name: 'Thử lại' }).click();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);

  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(await buyer.orders()).toHaveLength(1);
  expect(await seller.stock(product.id)).toBe(4);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});

// After a lost checkout response the form stays, so the buyer can press
// "Đặt hàng" again; the same key replays the committed checkout (former
// finding F3).
test('after a lost checkout response the buyer retries from the form and gets the same orders', async ({ page }) => {
  const { seller, buyer, product, keys } = await setup(page);
  await loseFirstCheckoutResponse(page);

  await placeOrderButton(page).click();
  await expect(page.getByRole('button', { name: 'Thử lại' })).toBeVisible();
  await expect(page.getByText('Không có sản phẩm nào để thanh toán')).toHaveCount(0);
  await expect(placeOrderButton(page)).toBeEnabled();
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);

  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(await buyer.orders()).toHaveLength(1);
  expect(await seller.stock(product.id)).toBe(4);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});
