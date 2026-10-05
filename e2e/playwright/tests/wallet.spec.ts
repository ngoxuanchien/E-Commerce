import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { addToCart, openCheckout, placeOrderButton, signIn } from '../support/ui';

test('seller earns balance from order and can request payout to SETTLED', async ({ page }) => {
  test.setTimeout(180_000);
  const run = runId();

  // Setup: Create seller and product
  const seller = await ApiUser.register(`Người bán ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(`Shop Ví ${run}`, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Sản phẩm ví test ${run}`, 5, 120_000);

  // Create buyer and place VNPAY order
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);
  await signIn(page, buyer.account);
  await page.goto(`/products/${product.id}`);
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();

  // Open checkout and select VNPAY payment
  await openCheckout(page);
  await page.getByTestId('payment-method-VNPAY').click();
  await placeOrderButton(page).click();

  // Go to FAKE payment page and complete payment
  await expect(page).toHaveURL(/\/api\/payments\/fake\/checkout\//);
  await page.getByTestId('fake-pay-succeed').click();

  // Wait for payment completion
  await expect(page).toHaveURL(/\/checkout\/result\?payment_id=/);
  await expect
    .poll(() => page.getByTestId('payment-result').getAttribute('data-status'), { timeout: 10_000 })
    .toBe('SUCCEEDED');

  // Get the order ID and wait for payment status to be PAID
  const orders = await buyer.orders();
  const order = orders[0];
  expect(order).toBeDefined();

  await expect
    .poll(async () => {
      const updated = await buyer.order(order.id);
      return updated.payment_status;
    }, { timeout: 20_000, intervals: [1000] })
    .toBe('PAID');

  // Seller confirms, ships, and delivers the order
  await seller.sellerAction(order.id, 'confirm');
  await seller.sellerAction(order.id, 'ship');
  await seller.sellerAction(order.id, 'deliver');

  // Buyer completes the order
  await buyer.complete(order.id);

  // Poll seller wallet until available balance > 0
  await expect
    .poll(async () => {
      const w = await seller.wallet();
      return w.available;
    }, { timeout: 30_000, intervals: [1000] })
    .toBeGreaterThan(0);

  // Get the available balance for the payout
  const walletBefore = await seller.wallet();
  const availableAmount = walletBefore.available;

  // Seller opens wallet page
  await signIn(page, seller.account);
  await page.goto('/seller/wallet');

  // Verify wallet-available shows the amount
  await expect(page.getByTestId('wallet-available')).toBeVisible({ timeout: 10_000 });
  await expect
    .poll(() => page.getByTestId('wallet-available').textContent(), { timeout: 10_000 })
    .toContain('₫');

  // Fill payout amount with full available amount on wallet page
  const payoutAmountInput = page.getByTestId('payout-amount');
  await expect(payoutAmountInput).toBeVisible({ timeout: 10_000 });
  await payoutAmountInput.fill(availableAmount.toString());

  // Submit payout request
  const submitButton = page.getByTestId('payout-submit');
  await expect(submitButton).toBeVisible({ timeout: 10_000 });
  await submitButton.click();

  // Wait for success and navigate to payouts page to see the row
  await page.waitForTimeout(1000);
  await page.goto('/seller/payouts');

  // Poll for payout status to reach SETTLED on payouts page
  await expect
    .poll(async () => {
      await page.reload();
      const row = page.getByTestId('payout-row').first();
      return row.getAttribute('data-status');
    }, { timeout: 30_000, intervals: [1000] })
    .toBe('SETTLED');

  // Verify via API that wallet-available is now 0 (payout settled and consumed the available balance)
  const walletAfter = await seller.wallet();
  expect(walletAfter.available).toBe(0);

  await Promise.all([seller.dispose(), buyer.dispose()]);
});
