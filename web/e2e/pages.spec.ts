import { expect, test } from '@playwright/test';
import { expectNoHorizontalScroll, login, search } from './helpers';

test('public pages: home, search, product, shop, login, register', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Nhiều sạp hàng, một giỏ hàng.' })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto('/search?category=gom-su&sort=price_desc');
  await page.getByRole('link', { name: 'Bình hoa gốm men rạn cổ' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Bình hoa gốm men rạn cổ' })).toBeVisible();
  await page.getByRole('link', { name: 'Xem shop' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Gốm Bát Tràng Cô Bình' })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto('/login');
  await expect(page.getByRole('heading', { level: 1, name: 'Đăng nhập' })).toBeVisible();
  await page.goto('/register');
  await expect(page.getByRole('heading', { level: 1, name: 'Tạo tài khoản' })).toBeVisible();
});

test('the variant selector works with the keyboard', async ({ page }) => {
  await page.goto('/');
  await search(page, 'ao thun tron');
  await page.getByRole('link', { name: 'Áo thun cotton trơn cổ tròn' }).click();
  const small = page.getByRole('radio', { name: 'S', exact: true });
  await expect(small).toBeChecked();
  await small.focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('radio', { name: 'M', exact: true })).toBeChecked();
  await expect(page.getByText('Còn 15 sản phẩm')).toBeVisible();
});

test('buyer order pages: detail, confirm received, cancel with a reason', async ({ page }) => {
  await login(page, 'an@example.com', 'An Nguyễn');
  await page.goto('/orders');
  await page.getByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' }).getByRole('link', { name: 'Xem chi tiết' }).click();
  await expect(page.getByRole('heading', { name: 'Lịch sử đơn hàng' })).toBeVisible();
  await page.getByRole('button', { name: 'Đã nhận được hàng' }).click();
  await expect(page.locator('[data-status]')).toHaveText('Hoàn thành');

  await page.goto('/orders?status=PENDING_CONFIRMATION');
  await page.getByRole('article', { name: 'Đơn hàng của Áo Thun Sài Gòn' }).getByRole('link', { name: 'Xem chi tiết' }).click();
  await page.getByRole('button', { name: 'Huỷ đơn hàng' }).click();
  const dialog = page.getByRole('dialog', { name: 'Huỷ đơn hàng' });
  await dialog.getByLabel('Lý do huỷ').fill('Đặt nhầm size');
  await dialog.getByRole('button', { name: 'Xác nhận huỷ' }).click();
  await expect(page.getByText('Lý do huỷ: Đặt nhầm size')).toBeVisible();
});

test('seller pages: products, edit form, orders, shop profile', async ({ page }) => {
  await login(page, 'binh@example.com', 'Bình Trần');
  await page.goto('/seller/products');
  await expect(page.getByRole('row', { name: /Bát ăn cơm men lam Bát Tràng/ })).toBeVisible();
  await page.getByRole('link', { name: 'Sửa Bát ăn cơm men lam Bát Tràng' }).click();
  await expect(page.getByLabel('Tên sản phẩm')).toHaveValue('Bát ăn cơm men lam Bát Tràng');
  const sellerNav = page.getByRole('navigation', { name: 'Kênh người bán' });
  await sellerNav.getByRole('link', { name: 'Đơn hàng' }).click();
  await expect(page.getByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' })).toBeVisible();
  await sellerNav.getByRole('link', { name: 'Hồ sơ shop' }).click();
  await page.getByLabel('Giới thiệu shop').fill('Gốm men lam Bát Tràng, giao toàn quốc.');
  await page.getByRole('button', { name: 'Lưu thay đổi' }).click();
  await expect(page.getByText('Đã lưu thay đổi')).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test('the mock session survives a reload', async ({ page }) => {
  await login(page, 'an@example.com', 'An Nguyễn');
  await page.reload();
  await expect(page.getByRole('button', { name: 'An Nguyễn' })).toBeVisible();
});
