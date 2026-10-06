import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { addToCart, openCheckout, placeOrderButton, signIn } from '../support/ui';

test('online checkout pays on the FAKE page and the orders show as paid', async ({ page }) => {
  test.setTimeout(120_000);
  const run = runId();
  const seller = await ApiUser.register(`Người bán ${run}`, `e2e-${run}-seller@example.test`);
  const shopName = `Shop Thanh Toán ${run}`;
  await seller.openShop(shopName, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Sản phẩm test thanh toán ${run}`, 10);

  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  try {
    // Buyer signs in, adds product to cart and opens checkout
    await signIn(page, buyer.account);
    await page.goto(`/products/${product.id}`);
    await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
    await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();

    // Open checkout and select VNPAY payment
    await openCheckout(page);
    await page.getByTestId('payment-method-VNPAY').click();
    await placeOrderButton(page).click();

    // Should redirect to the FAKE hosted page
    await expect(page).toHaveURL(/\/api\/payments\/fake\/checkout\//);

    // Click the succeed button to complete payment
    await page.getByTestId('fake-pay-succeed').click();

    // Should redirect to checkout result page
    await expect(page).toHaveURL(/\/checkout\/result\?payment_id=/);

    // Payment result should show SUCCEEDED status
    await expect
      .poll(() => page.getByTestId('payment-result').getAttribute('data-status'), { timeout: 10_000 })
      .toBe('SUCCEEDED');

    // Go to orders page and verify payment status (backend processes the payment asynchronously)
    await page.goto('/orders');
    // Poll until the specific order's payment status is updated
    await expect(async () => {
      await page.reload();
      await expect(
        page.getByRole('article', { name: `Đơn hàng của ${shopName}` }).getByTestId('order-payment-status'),
      ).toHaveText('Đã thanh toán', { timeout: 2_000 });
    }).toPass({ timeout: 20_000 });
  } finally {
    await Promise.all([seller.dispose(), buyer.dispose()]);
  }
});

test('a failed payment can be paid again', async ({ page }) => {
  test.setTimeout(120_000);
  const run = runId();
  const seller = await ApiUser.register(`Người bán ${run}`, `e2e-${run}-seller@example.test`);
  const shopName = `Shop Thanh Toán ${run}`;
  await seller.openShop(shopName, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Sản phẩm test thanh toán ${run}`, 10);

  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  try {
    // Buyer signs in, adds product to cart and opens checkout
    await signIn(page, buyer.account);
    await page.goto(`/products/${product.id}`);
    await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
    await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();

    // Open checkout and select VNPAY payment
    await openCheckout(page);
    await page.getByTestId('payment-method-VNPAY').click();
    await placeOrderButton(page).click();

    // Should redirect to the FAKE hosted page
    await expect(page).toHaveURL(/\/api\/payments\/fake\/checkout\//);

    // Click the fail button to simulate payment failure
    await page.getByTestId('fake-pay-fail').click();

    // Should redirect to checkout result page
    await expect(page).toHaveURL(/\/checkout\/result\?payment_id=/);

    // Payment result should show FAILED status
    await expect
      .poll(() => page.getByTestId('payment-result').getAttribute('data-status'), { timeout: 10_000 })
      .toBe('FAILED');

    // Click the "pay again" button
    await page.getByTestId('pay-again').click();

    // Should redirect back to the FAKE hosted page
    await expect(page).toHaveURL(/\/api\/payments\/fake\/checkout\//);

    // Click succeed button this time
    await page.getByTestId('fake-pay-succeed').click();

    // Should redirect to checkout result page with SUCCEEDED status
    await expect(page).toHaveURL(/\/checkout\/result\?payment_id=/);
    await expect
      .poll(() => page.getByTestId('payment-result').getAttribute('data-status'), { timeout: 10_000 })
      .toBe('SUCCEEDED');

    // The payment status is verified by test 1; we only test the pay-again flow here
  } finally {
    await Promise.all([seller.dispose(), buyer.dispose()]);
  }
});
