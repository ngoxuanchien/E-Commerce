import path from 'node:path';
import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { addToCart, loginViaUi, logout, openCheckout, placeOrderButton, registerViaUi, search, signIn } from '../support/ui';

test('seller opens a shop and lists a product; buyer buys from two shops; the order completes', async ({ page }) => {
  test.setTimeout(240_000);
  const run = runId();
  const seller = { email: `e2e-${run}-seller@example.test`, fullName: `Người bán ${run}` };
  const shopName = `Gốm Sứ ${run}`;
  const productName = `Bình gốm men lam ${run}`;

  // A second shop and a buyer, prepared through the API.
  const sellerB = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller-b@example.test`);
  await sellerB.openShop(`Đèn Gỗ ${run}`, `e2e-${run}-b`);
  const productB = await sellerB.createProduct(`Đèn ngủ gỗ thông ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  // 1. A new user registers and opens a shop, becoming a seller.
  await registerViaUi(page, seller);
  await loginViaUi(page, seller);
  await page.goto('/seller');
  await page.getByRole('link', { name: 'Mở shop của bạn' }).click();
  await page.getByLabel('Tên shop').fill(shopName);
  await expect(page.getByLabel('Địa chỉ shop')).toHaveValue(`gom-su-${run}`);
  await page.getByLabel('Giới thiệu shop').fill('Gốm men lam thủ công.');
  await page.getByRole('button', { name: 'Mở shop' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);

  // 2. The seller lists a product with a real image upload and two variants.
  await page.getByRole('link', { name: 'Thêm sản phẩm', exact: true }).click();
  await page.getByLabel('Danh mục').selectOption({ label: 'Trang trí nhà cửa' });
  await page.getByLabel('Tên sản phẩm').fill(productName);
  await page.getByLabel('Mô tả').fill('Men lam vẽ tay, cao 30 cm.');
  await page.getByLabel('Thêm ảnh').setInputFiles(path.join(__dirname, '..', '..', 'fixtures', 'images', 'home-03.jpg'));
  await expect(page.getByRole('img', { name: 'Ảnh 1' })).toBeVisible();
  await page.getByLabel('Tên phân loại 1').fill('Bộ nhỏ');
  await page.getByLabel('Giá (₫) phân loại 1').fill('250000');
  await page.getByLabel('Tồn kho phân loại 1').fill('5');
  await page.getByRole('button', { name: 'Thêm phân loại' }).click();
  await page.getByLabel('Tên phân loại 2').fill('Bộ lớn');
  await page.getByLabel('Giá (₫) phân loại 2').fill('390000');
  await page.getByLabel('Tồn kho phân loại 2').fill('3');
  await page.getByRole('button', { name: 'Tạo sản phẩm' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);
  await expect(page.getByRole('row', { name: new RegExp(productName) })).toBeVisible();
  await logout(page, seller.fullName);

  // 3. The buyer searches without diacritics and sees the uploaded image.
  await signIn(page, buyer.account);
  await search(page, `binh gom men lam ${run}`);
  const card = page.getByRole('listitem').filter({ hasText: productName });
  await expect(card.locator('img')).toHaveAttribute('src', /\/product-images\/shops\//);
  await expect.poll(() => card.locator('img').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
  await addToCart(page, productName, 'Bộ lớn');
  await search(page, `den ngu go thong ${run}`);
  await addToCart(page, productB.name);

  // 4. Checkout splits the cart into one COD order per shop.
  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await expect(page.getByRole('region', { name: `Shop ${shopName}` })).toBeVisible();
  await expect(page.getByRole('region', { name: `Shop Đèn Gỗ ${run}` })).toBeVisible();
  await openCheckout(page);
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=2$/);
  await expect(page.getByRole('article', { name: `Đơn hàng của ${shopName}` })).toBeVisible();
  await expect(page.getByRole('article', { name: `Đơn hàng của Đèn Gỗ ${run}` })).toBeVisible();
  await logout(page, buyer.account.fullName);

  // 5. The seller confirms, ships and delivers.
  await signIn(page, seller);
  await page.goto('/seller/orders');
  const order = page.getByRole('article', { name: `Đơn hàng của ${shopName}` });
  await order.getByRole('button', { name: 'Xác nhận đơn' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã xác nhận');
  await order.getByRole('button', { name: 'Bắt đầu giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đang giao');
  await order.getByRole('button', { name: 'Xác nhận đã giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã giao');
  await logout(page, seller.fullName);

  // 6. The buyer confirms receipt — unless the order already auto-completed
  //    (the e2e stack runs AUTO_COMPLETE_AFTER=5s; see e2e/README.md).
  await signIn(page, buyer.account);
  await page.goto('/orders');
  await page.getByRole('article', { name: `Đơn hàng của ${shopName}` }).getByRole('link', { name: 'Xem chi tiết' }).click();
  const receive = page.getByRole('button', { name: 'Đã nhận được hàng' });
  if (await receive.waitFor({ state: 'visible', timeout: 3_000 }).then(() => true, () => false)) {
    await receive.click();
  }
  await expect(async () => {
    await page.reload();
    await expect(page.locator('[data-status]').first()).toHaveText('Hoàn thành', { timeout: 2_000 });
  }).toPass({ timeout: 30_000 });

  await Promise.all([sellerB.dispose(), buyer.dispose()]);
});
