import { type Page, expect } from '@playwright/test';

export const PASSWORD = 'matkhau123';

export async function register(page: Page, fullName: string, email: string) {
  await page.goto('/register');
  await page.getByLabel('Họ và tên').fill(fullName);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Mật khẩu', { exact: true }).fill(PASSWORD);
  await page.getByLabel('Nhập lại mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Tạo tài khoản' }).click();
  await expect(page.getByText('Tạo tài khoản thành công. Mời bạn đăng nhập.')).toBeVisible();
}

export async function login(page: Page, email: string, fullName: string) {
  if (!page.url().includes('/login')) await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Đăng nhập' }).click();
  await expect(page.getByRole('button', { name: fullName })).toBeVisible();
}

export async function logout(page: Page, fullName: string) {
  await page.getByRole('button', { name: fullName }).click();
  await page.getByRole('button', { name: 'Đăng xuất' }).click();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
}

export async function search(page: Page, q: string) {
  const box = page.getByRole('searchbox', { name: 'Tìm sản phẩm' });
  await box.fill(q);
  await box.press('Enter');
  await expect(page).toHaveURL(/\/search\?q=/);
}

export async function addToCart(page: Page, productName: string, variant?: string) {
  await page.getByRole('link', { name: productName }).click();
  await expect(page.getByRole('heading', { level: 1, name: productName })).toBeVisible();
  if (variant) await page.getByRole('radio', { name: variant, exact: true }).check();
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();
}

export async function checkout(page: Page, method: 'COD' | 'VNPAY' | 'STRIPE' = 'COD') {
  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await page.getByRole('button', { name: 'Mua hàng' }).click();
  await page.getByLabel('Số điện thoại').fill('0901234567');
  await page.getByLabel('Số nhà, tên đường').fill('12 Lý Tự Trọng');
  await page.getByLabel('Phường/Xã').fill('Bến Nghé');
  await page.getByLabel('Quận/Huyện').fill('Quận 1');
  await page.getByLabel('Tỉnh/Thành phố').fill('TP. Hồ Chí Minh');
  if (method !== 'COD') await page.getByTestId('payment-method-' + method).check();
  await page.getByRole('button', { name: /^Đặt hàng/ }).click();
}

export async function expectNoHorizontalScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}
