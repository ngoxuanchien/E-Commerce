import { type Page, expect } from '@playwright/test';
import { type Account, GATEWAY_URL, PASSWORD, withRateLimitRetry } from './api';

export const OUT_OF_STOCK_MESSAGE = 'Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.';

/**
 * Logs in without the form: the login call goes straight to the gateway, and
 * its HttpOnly refresh cookie (host "localhost", path /api/auth; cookies
 * ignore ports) is then sent by the web app on localhost:3000. The app
 * restores the session on load like a returning visitor. This keeps the web
 * origin's 10/min login bucket for the tests that exercise the form itself.
 */
export async function signIn(page: Page, account: Account): Promise<void> {
  const res = await withRateLimitRetry(() =>
    page.request.post(`${GATEWAY_URL}/api/auth/login`, { data: { email: account.email, password: PASSWORD } }),
  );
  expect(res.status(), await res.text()).toBe(200);
  await page.goto('/');
  await expect(page.getByRole('button', { name: account.fullName })).toBeVisible();
}

export async function registerViaUi(page: Page, account: Account): Promise<void> {
  await page.goto('/register');
  await page.getByLabel('Họ và tên').fill(account.fullName);
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Mật khẩu', { exact: true }).fill(PASSWORD);
  await page.getByLabel('Nhập lại mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Tạo tài khoản' }).click();
  await expect(page.getByText('Tạo tài khoản thành công. Mời bạn đăng nhập.')).toBeVisible();
}

export async function loginViaUi(page: Page, account: Account): Promise<void> {
  if (!page.url().includes('/login')) await page.goto('/login');
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Đăng nhập' }).click();
  await expect(page.getByRole('button', { name: account.fullName })).toBeVisible();
}

export async function logout(page: Page, fullName: string): Promise<void> {
  await page.getByRole('button', { name: fullName }).click();
  await page.getByRole('button', { name: 'Đăng xuất' }).click();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
}

export async function search(page: Page, q: string): Promise<void> {
  const box = page.getByRole('searchbox', { name: 'Tìm sản phẩm' });
  await box.fill(q);
  await box.press('Enter');
  await expect(page).toHaveURL(/\/search\?q=/);
}

export async function addToCart(page: Page, productName: string, variant?: string): Promise<void> {
  await page.getByRole('link', { name: productName }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: productName })).toBeVisible();
  if (variant) await page.getByRole('radio', { name: variant, exact: true }).check();
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();
}

/** Goes from the cart to the checkout page and fills the shipping address. */
export async function openCheckout(page: Page): Promise<void> {
  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await page.getByRole('button', { name: 'Mua hàng' }).click();
  await page.getByLabel('Số điện thoại').fill('0901234567');
  await page.getByLabel('Số nhà, tên đường').fill('12 Lý Tự Trọng');
  await page.getByLabel('Phường/Xã').fill('Bến Nghé');
  await page.getByLabel('Quận/Huyện').fill('Quận 1');
  await page.getByLabel('Tỉnh/Thành phố').fill('TP. Hồ Chí Minh');
}

export function placeOrderButton(page: Page) {
  return page.getByRole('button', { name: 'Đặt hàng' });
}
