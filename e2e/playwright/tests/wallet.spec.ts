import { expect, test } from '@playwright/test';
import { ApiUser, type Order, runId } from '../support/api';
import { signIn } from '../support/ui';

test('seller earns balance from order and can request payout to SETTLED', async ({ page }) => {
  test.setTimeout(180_000);
  const run = runId();

  const seller = await ApiUser.register(`Người bán ${run}`, `e2e-${run}-seller@example.test`);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  try {
    // Setup: Create seller and product
    await seller.openShop(`Shop Ví ${run}`, `e2e-${run}-shop`);
    const product = await seller.createProduct(`Sản phẩm ví test ${run}`, 5, 120_000);

    // Buyer: pay through the API (cart -> VNPAY checkout -> FAKE hosted pay -> succeed)
    await buyer.setCartItem(product.skus[0].id, 1);
    const checkout = await buyer.checkout('VNPAY');
    const payment = await buyer.startPayment(checkout.id);
    await buyer.fakeSubmit(payment.payment_id, 'succeed');

    // The webhook/consumer flips the order to PAID asynchronously
    let order: Order | undefined;
    await expect
      .poll(
        async () => {
          order = (await buyer.orders()).find((o) => o.checkout_id === checkout.id);
          return order?.payment_status;
        },
        { timeout: 20_000, intervals: [1000] },
      )
      .toBe('PAID');
    if (!order) throw new Error('Order not found');

    // Seller: Confirm, ship, deliver
    await seller.sellerAction(order.id, 'confirm');
    await seller.sellerAction(order.id, 'ship');
    await seller.sellerAction(order.id, 'deliver');

    // Buyer: Complete order
    await buyer.complete(order.id);

    // Poll seller wallet until available balance > 0
    await expect
      .poll(async () => {
        const w = await seller.wallet();
        return w.available;
      }, { timeout: 30_000, intervals: [1000] })
      .toBeGreaterThan(0);

    // Assert exact credited amount: price 120,000 + shipping 30,000 - commission floor(120,000×5%) = 144,000
    const walletBefore = await seller.wallet();
    const availableAmount = walletBefore.available;
    expect(availableAmount).toBe(144_000);

    const expectedFormattedAmount = new Intl.NumberFormat('vi-VN', {
      style: 'currency',
      currency: 'VND',
    }).format(availableAmount);

    // Seller: Open wallet page and verify exact balance is displayed (144.000 ₫)
    await signIn(page, seller.account);
    await page.goto('/seller/wallet');
    await expect(page.getByTestId('wallet-available').locator('.price')).toHaveText(expectedFormattedAmount);

    // Fill payout amount and submit
    const payoutAmountInput = page.getByTestId('payout-amount');
    await expect(payoutAmountInput).toBeVisible({ timeout: 10_000 });
    await payoutAmountInput.fill(availableAmount.toString());

    const submitButton = page.getByTestId('payout-submit');
    await expect(submitButton).toBeVisible({ timeout: 10_000 });
    await submitButton.click();

    // Assert payout row appeared (in payouts page)
    await page.goto('/seller/payouts');
    await expect(page.getByTestId('payout-row').first()).toBeVisible({ timeout: 10_000 });

    // Poll for payout status to reach SETTLED with bounded retry
    await expect(async () => {
      await page.reload();
      await expect(page.getByTestId('payout-row').first()).toHaveAttribute('data-status', 'SETTLED', {
        timeout: 2_000,
      });
    }).toPass({ timeout: 30_000 });

    // Verify wallet-available shows exactly "0 ₫" (bounded retry)
    await page.goto('/seller/wallet');
    await expect(async () => {
      await page.reload();
      await expect(page.getByTestId('wallet-available').locator('.price')).toHaveText(/^\s*0\s*₫\s*$/, { timeout: 2_000 });
    }).toPass({ timeout: 30_000 });
  } finally {
    await Promise.all([seller.dispose(), buyer.dispose()]);
  }
});
