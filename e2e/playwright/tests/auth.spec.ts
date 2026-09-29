import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { loginViaUi, logout, signIn } from '../support/ui';

test('the session survives a reload on the real gateway; after logout /orders redirects to login', async ({ page, context }) => {
  const run = runId();
  const user = await ApiUser.register(`Khách ${run}`, `e2e-${run}-auth@example.test`);

  await loginViaUi(page, user.account);
  const cookie = (await context.cookies()).find((c) => c.name === 'refresh_token');
  expect(cookie).toMatchObject({ httpOnly: true, path: '/api/auth' });

  const refreshed = page.waitForResponse((r) => r.url().endsWith('/api/auth/refresh') && r.status() === 200);
  await page.reload();
  await refreshed;
  await expect(page.getByRole('button', { name: user.account.fullName })).toBeVisible();

  await logout(page, user.account.fullName);
  await page.reload();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
  await page.goto('/orders');
  await expect(page).toHaveURL(/\/login\?next=%2Forders$/);
  await user.dispose();
});

test('a buyer cannot open the seller center', async ({ page }) => {
  const run = runId();
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  await signIn(page, buyer.account);
  await page.goto('/seller/products');
  await expect(page.getByRole('heading', { name: 'Bạn chưa có shop' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Thêm sản phẩm', exact: true })).toHaveCount(0);
  expect(await buyer.status('/api/seller/products')).toBe(403);
  await buyer.dispose();
});
