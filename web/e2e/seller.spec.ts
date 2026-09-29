import { expect, test } from '@playwright/test';
import { addToCart, checkout, login, logout, register, search } from './helpers';

test('a new seller opens a shop, lists a product with an image and fulfils an order', async ({ page }) => {
  const email = `dung.${Date.now()}@example.com`;
  await register(page, 'Dũng Võ', email);
  await login(page, email, 'Dũng Võ');

  await page.goto('/seller');
  await page.getByRole('link', { name: 'Mở shop của bạn' }).click();
  await page.getByLabel('Tên shop').fill('Đồ Da Dũng Võ');
  await expect(page.getByLabel('Địa chỉ shop')).toHaveValue('do-da-dung-vo');
  await page.getByLabel('Giới thiệu shop').fill('Ví và thắt lưng da bò thủ công.');
  await page.getByRole('button', { name: 'Mở shop' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);

  await page.getByRole('link', { name: 'Thêm sản phẩm', exact: true }).click();
  await page.getByLabel('Danh mục').selectOption({ label: 'Phụ kiện điện thoại' });
  await page.getByLabel('Tên sản phẩm').fill('Ví da bò khắc tên');
  await page.getByLabel('Mô tả').fill('Da bò thật, khắc tên miễn phí.');
  await page.getByLabel('Thêm ảnh').setInputFiles('e2e/fixtures/product.png');
  await expect(page.getByRole('img', { name: 'Ảnh 1' })).toBeVisible();
  await page.getByLabel('Tên phân loại 1').fill('Màu nâu');
  await page.getByLabel('Giá (₫) phân loại 1').fill('250000');
  await page.getByLabel('Tồn kho phân loại 1').fill('10');
  await page.getByRole('button', { name: 'Tạo sản phẩm' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);
  await expect(page.getByRole('row', { name: /Ví da bò khắc tên/ })).toBeVisible();
  await logout(page, 'Dũng Võ');

  await login(page, 'an@example.com', 'An Nguyễn');
  await search(page, 'vi da bo');
  // The uploaded file went through the presigned-POST mock and is served back as the product image.
  await expect(page.getByRole('listitem').filter({ hasText: 'Ví da bò khắc tên' }).locator('img')).toHaveAttribute('src', /^data:image\/png/);
  await addToCart(page, 'Ví da bò khắc tên');
  await checkout(page);
  await expect(page).toHaveURL(/\/orders\?placed=1$/);
  await logout(page, 'An Nguyễn');

  await login(page, email, 'Dũng Võ');
  await page.goto('/seller/orders');
  const order = page.getByRole('article', { name: 'Đơn hàng của Đồ Da Dũng Võ' });
  await order.getByRole('button', { name: 'Xác nhận đơn' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã xác nhận');
  await order.getByRole('button', { name: 'Bắt đầu giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đang giao');
  await order.getByRole('button', { name: 'Xác nhận đã giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã giao');
});
