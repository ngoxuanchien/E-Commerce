import { type Page, expect, test } from '@playwright/test';
import { ApiUser, WEB_URL, runId } from '../support/api';
import { OUT_OF_STOCK_MESSAGE, openCheckout, placeOrderButton, signIn } from '../support/ui';

test('two buyers race for the last unit: exactly one order is placed', async ({ browser }) => {
  const run = runId();
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(`Hàng Hiếm ${run}`, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Tranh sơn mài độc bản ${run}`, 1);

  const buyers: ApiUser[] = [];
  for (const i of [1, 2]) buyers.push(await ApiUser.register(`Người mua ${i} ${run}`, `e2e-${run}-buyer${i}@example.test`));

  const pages: Page[] = [];
  for (const b of buyers) {
    await b.setCartItem(product.skus[0].id, 1);
    const context = await browser.newContext({ baseURL: WEB_URL, locale: 'vi-VN' });
    const page = await context.newPage();
    await signIn(page, b.account);
    await openCheckout(page);
    pages.push(page);
  }

  await Promise.all(pages.map((p) => placeOrderButton(p).click()));
  await Promise.all(pages.map((p) => p.waitForURL(/\/orders\?placed=1$|\/cart$/)));

  const winners = pages.filter((p) => /\/orders\?placed=1$/.test(p.url()));
  const losers = pages.filter((p) => /\/cart$/.test(p.url()));
  expect(winners).toHaveLength(1);
  expect(losers).toHaveLength(1);
  await expect(losers[0].getByText(OUT_OF_STOCK_MESSAGE)).toBeVisible();
  expect(await seller.stock(product.id)).toBe(0);

  await Promise.all(pages.map((p) => p.context().close()));
  await Promise.all([seller.dispose(), ...buyers.map((b) => b.dispose())]);
});
