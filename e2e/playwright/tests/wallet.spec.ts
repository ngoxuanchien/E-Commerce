import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { openCheckout, placeOrderButton, signIn } from '../support/ui';

test('seller earns balance from order and can request payout to SETTLED', async ({ page }) => {
  test.setTimeout(180_000);
  const run = runId();

  const seller = await ApiUser.register(`Người bán ${run}`, `e2e-${run}-seller@example.test`);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  try {
    // Setup: Create seller and product
    await seller.openShop(`Shop Ví ${run}`, `e2e-${run}-shop`);
    const product = await seller.createProduct(`Sản phẩm ví test ${run}`, 5, 120_000);

    // Buyer: Checkout and payment via UI (exercises fixed fakeSubmit when redirect happens)
    await signIn(page, buyer.account);
    await page.goto(`/products/${product.id}`);
    await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
    await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();

    await openCheckout(page);
    await page.getByTestId('payment-method-VNPAY').click();
    await placeOrderButton(page).click();

    // Should redirect to the FAKE hosted page (exercises the fixed fakeSubmit endpoint)
    await expect(page).toHaveURL(/\/api\/payments\/fake\/checkout\//);
    await page.getByTestId('fake-pay-succeed').click();

    // Wait for payment completion
    await expect(page).toHaveURL(/\/checkout\/result\?payment_id=/);

    // Get the order and poll payment_status to PAID
    const orders = await buyer.orders();
    const order = orders[0];
    expect(order).toBeDefined();

    await expect
      .poll(async () => {
        const updated = await buyer.order(order.id);
        return updated.payment_status;
      }, { timeout: 20_000, intervals: [1000] })
      .toBe('PAID');

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

    // Get the available balance for the payout
    const walletBefore = await seller.wallet();
    const availableAmount = walletBefore.available;
    const expectedFormattedAmount = new Intl.NumberFormat('vi-VN', {
      style: 'currency',
      currency: 'VND',
    }).format(availableAmount);

    // Seller: Open wallet page and verify balance is displayed
    await signIn(page, seller.account);
    await page.goto('/seller/wallet');
    await expect(page.getByTestId('wallet-available')).toContainText(expectedFormattedAmount);

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

    // Verify wallet-available shows "0 ₫" via UI (bounded retry)
    await page.goto('/seller/wallet');
    await expect(async () => {
      await page.reload();
      await expect(page.getByTestId('wallet-available')).toContainText('0 ₫', { timeout: 2_000 });
    }).toPass({ timeout: 10_000 });
  } finally {
    await Promise.all([seller.dispose(), buyer.dispose()]);
  }
});
