import { expect, test } from '@playwright/test';
import { login } from './helpers';

test('the session survives a reload through the HttpOnly refresh cookie; JS stores no token', async ({ page, context }) => {
  await login(page, 'an@example.com', 'An Nguyễn');

  const refreshCookie = (await context.cookies()).find((c) => c.name === 'refresh_token');
  expect(refreshCookie).toMatchObject({ httpOnly: true, path: '/api/auth' });
  expect(await page.evaluate(() => document.cookie)).not.toContain('refresh_token');

  const refreshed = page.waitForResponse((r) => r.url().endsWith('/api/auth/refresh') && r.status() === 200);
  await page.reload();
  await refreshed;
  await expect(page.getByRole('button', { name: 'An Nguyễn' })).toBeVisible();

  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
  expect(stored).not.toContain('stub-at-');
});

test('after logout a reload stays logged out and protected pages redirect to login', async ({ page }) => {
  await login(page, 'an@example.com', 'An Nguyễn');
  await page.getByRole('button', { name: 'An Nguyễn' }).click();
  await page.getByRole('button', { name: 'Đăng xuất' }).click();
  await page.reload();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
  await page.goto('/orders');
  await expect(page).toHaveURL(/\/login\?next=%2Forders$/);
});
