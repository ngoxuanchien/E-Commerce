import { expect, test } from '@playwright/test';
import { login } from './helpers';

test('seller sees the wallet, requests a payout and sees it settle', async ({ page }) => {
  await login(page, 'binh@example.com', 'Bình Trần');
  await page.goto('/seller/wallet');
  await expect(page.getByTestId('wallet-available')).toContainText('457.500');

  await page.getByTestId('payout-amount').fill('100000');
  const submitButton = page.getByTestId('payout-submit');
  const [resp] = await Promise.all([
    page.waitForResponse(r => r.url().endsWith('/api/seller/payouts') && r.request().method() === 'POST'),
    submitButton.click()
  ]);
  expect(resp.status()).toBe(201);
  await expect(page.getByText('Đã gửi yêu cầu rút tiền')).toBeVisible();

  await page.goto('/seller/payouts');
  const row = page.getByTestId('payout-row').first();
  await expect(row).toHaveAttribute('data-status', 'REQUESTED');
  await expect(row).toHaveAttribute('data-status', 'SETTLED', { timeout: 15_000 });
});
