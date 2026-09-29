import { beforeEach, describe, expect, test } from 'vitest';
import type { CheckoutRequest } from '@/api/types';
import { MockDb, SHIPPING_FEE_FLAT } from './db';
import { SEED } from './seed';

let db: MockDb;
beforeEach(() => {
  db = new MockDb();
});

function caught(fn: () => unknown): unknown {
  try {
    fn();
  } catch (err) {
    return err;
  }
  throw new Error('expected an error');
}

const address = { full_name: 'An Nguyễn', phone: '0901234567', line1: '12 Lý Tự Trọng', ward: 'Bến Nghé', district: 'Quận 1', province: 'TP. Hồ Chí Minh' };
const body = (sku_ids?: string[]): CheckoutRequest => ({ ...(sku_ids ? { sku_ids } : {}), shipping_address: address, payment_method: 'COD' });
const skuStock = (productId: string, skuId: string) => db.product(productId).skus.find((s) => s.id === skuId)!.stock;

describe('identity', () => {
  test('login issues a token whose roles are a snapshot; refresh re-reads roles', () => {
    const user = db.register({ email: ' Dung@Example.com ', password: 'matkhau123', full_name: 'Dũng Võ' });
    expect(user.email).toBe('dung@example.com');
    const first = db.login({ email: 'dung@example.com', password: 'matkhau123' });
    db.createShop(user.id, { name: 'Đồ Da Dũng Võ', slug: 'do-da-dung-vo', description: '' });
    expect(db.grant(`Bearer ${first.access_token}`)!.roles).not.toContain('seller');
    expect(db.refresh().user.roles).toContain('seller');
  });

  test('duplicate email → CONFLICT; bad password → UNAUTHENTICATED; refresh without session → UNAUTHENTICATED', () => {
    expect(caught(() => db.register({ email: 'an@example.com', password: 'matkhau123', full_name: 'X' }))).toMatchObject({ status: 409, code: 'CONFLICT' });
    expect(caught(() => db.login({ email: 'an@example.com', password: 'sai' }))).toMatchObject({ status: 401 });
    expect(caught(() => db.refresh())).toMatchObject({ status: 401, code: 'UNAUTHENTICATED' });
  });

  test('seller routes need the seller role in the token', () => {
    const t = db.login({ email: 'an@example.com', password: SEED.password });
    expect(caught(() => db.requireGrant(`Bearer ${t.access_token}`, 'seller'))).toMatchObject({ status: 403, code: 'FORBIDDEN' });
    expect(caught(() => db.requireGrant(null))).toMatchObject({ status: 401 });
  });
});

describe('catalog', () => {
  test('search is accent-insensitive and hides drafts', () => {
    expect(db.search({ q: 'bat an com' }).items.map((p) => p.name)).toEqual(['Bát ăn cơm men lam Bát Tràng']);
    expect(db.search({ q: 'ÁO THUN' }).items).toHaveLength(2);
    expect(db.search({ q: 'polo' }).items).toEqual([]);
  });

  test('category filter includes children; price filter uses min_price', () => {
    expect(db.search({ category: 'thoi-trang' }).items).toHaveLength(3);
    expect(db.search({ category: 'gom-su', max_price: 100_000 }).items.map((p) => p.name)).toEqual(['Bát ăn cơm men lam Bát Tràng']);
  });

  test('cursor pagination returns every product exactly once', () => {
    const seen: string[] = [];
    let cursor: string | undefined;
    do {
      const page = db.search({ sort: 'price_asc', limit: 2, cursor });
      seen.push(...page.items.map((p) => p.id));
      cursor = page.next_cursor ?? undefined;
    } while (cursor);
    expect(seen).toHaveLength(7);
    expect(new Set(seen).size).toBe(7);
  });

  test('non-active products are visible only to their owner', () => {
    expect(caught(() => db.product(SEED.products.polo))).toMatchObject({ status: 404 });
    expect(db.product(SEED.products.polo, SEED.users.chi).status).toBe('draft');
  });

  test('shop slug rules: pattern, reserved words, uniqueness, one shop per user', () => {
    const u = db.register({ email: 'e@example.com', password: 'matkhau123', full_name: 'E' });
    expect(caught(() => db.createShop(u.id, { name: 'Shop E', slug: 'me' }))).toMatchObject({ code: 'VALIDATION_ERROR' });
    expect(caught(() => db.createShop(u.id, { name: 'Shop E', slug: 'Có Dấu' }))).toMatchObject({ code: 'VALIDATION_ERROR' });
    expect(caught(() => db.createShop(u.id, { name: 'Shop E', slug: 'gom-bat-trang' }))).toMatchObject({ code: 'CONFLICT' });
    expect(caught(() => db.createShop(SEED.users.binh, { name: 'Shop 2', slug: 'shop-hai' }))).toMatchObject({ code: 'CONFLICT' });
  });

  test('product validation reports JSON paths per field', () => {
    const err = caught(() =>
      db.createProduct(SEED.users.binh, { category_id: 'nope', name: 'abc', status: 'active', skus: [{ name: '', price: 10, stock: -1 }] }),
    );
    expect(err).toMatchObject({ code: 'VALIDATION_ERROR' });
    expect((err as { details: { fields: Record<string, string> } }).details.fields).toMatchObject({
      category_id: expect.any(String),
      name: expect.any(String),
      'skus[0].name': expect.any(String),
      'skus[0].price': expect.any(String),
      'skus[0].stock': expect.any(String),
    });
  });

  test('PATCH skus: with id → update, without → create, omitted → deactivated; images must belong to the shop', () => {
    const p = db.product(SEED.products.bowl);
    const updated = db.updateProduct(SEED.users.binh, p.id, { skus: [{ id: SEED.skus.bowl11, name: 'Đường kính 11 cm', price: 50_000, stock: 5 }, { name: 'Đường kính 15 cm', price: 65_000, stock: 3 }] });
    expect(updated.skus.find((s) => s.id === SEED.skus.bowl11)).toMatchObject({ price: 50_000, active: true });
    expect(updated.skus.find((s) => s.id === SEED.skus.bowl13)).toMatchObject({ active: false });
    expect(updated.skus).toHaveLength(3);
    expect(caught(() => db.updateProduct(SEED.users.binh, p.id, { image_keys: [`shops/${SEED.shops.aoThun}/x.png`] }))).toMatchObject({ code: 'VALIDATION_ERROR' });
    expect(caught(() => db.updateProduct(SEED.users.chi, p.id, { name: 'Tên mới dài' }))).toMatchObject({ status: 404 });
  });
});

describe('cart and checkout', () => {
  test('own-shop SKUs and invalid quantities are rejected', () => {
    expect(caught(() => db.setCartItem(SEED.users.binh, SEED.skus.bowl11, 1))).toMatchObject({ code: 'CONFLICT' });
    expect(caught(() => db.setCartItem(SEED.users.an, SEED.skus.bowl11, 100))).toMatchObject({ code: 'VALIDATION_ERROR' });
    expect(caught(() => db.setCartItem(SEED.users.an, SEED.skus.teeXL, 1))).toMatchObject({ code: 'CONFLICT' });
  });

  test('checkout splits by shop, reserves stock, clears purchased lines and replays idempotently', () => {
    db.setCartItem(SEED.users.an, SEED.skus.bowl11, 2);
    db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
    const before = skuStock(SEED.products.bowl, SEED.skus.bowl11);

    const res = db.checkout(SEED.users.an, 'key-1', body());

    expect(res.orders).toHaveLength(2);
    expect(new Set(res.orders.map((o) => o.checkout_id)).size).toBe(1);
    const bowlOrder = res.orders.find((o) => o.shop.id === SEED.shops.gom)!;
    expect(bowlOrder).toMatchObject({ status: 'PENDING_CONFIRMATION', subtotal: 90_000, shipping_fee: SHIPPING_FEE_FLAT, total: 120_000 });
    expect(bowlOrder).not.toHaveProperty('seller_user_id');
    expect(skuStock(SEED.products.bowl, SEED.skus.bowl11)).toBe(before - 2);
    expect(db.cart(SEED.users.an).shops).toEqual([]);
    expect(db.checkout(SEED.users.an, 'key-1', body())).toEqual(res);
    expect(caught(() => db.checkout(SEED.users.an, 'key-1', body([SEED.skus.teeS])))).toMatchObject({ status: 422, code: 'IDEMPOTENCY_KEY_REUSED' });
  });

  test('insufficient stock → OUT_OF_STOCK with shortages and nothing reserved', () => {
    db.setCartItem(SEED.users.an, SEED.skus.vase, 2);
    db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
    const err = caught(() => db.checkout(SEED.users.an, 'key-2', body()));
    expect(err).toMatchObject({ status: 409, code: 'OUT_OF_STOCK', details: { shortages: [{ sku_id: SEED.skus.vase, requested: 2, available: 1 }] } });
    expect(skuStock(SEED.products.tee, SEED.skus.teeM)).toBe(15);
    expect(db.cart(SEED.users.an).shops).toHaveLength(2);
  });

  test('only selected sku_ids are bought; missing address fields → VALIDATION_ERROR paths', () => {
    db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
    db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
    expect(db.checkout(SEED.users.an, 'key-3', body([SEED.skus.teeM])).orders).toHaveLength(1);
    expect(db.cart(SEED.users.an).shops.map((s) => s.shop.id)).toEqual([SEED.shops.gom]);
    const err = caught(() => db.checkout(SEED.users.an, 'key-4', { ...body(), shipping_address: { ...address, phone: ' ' } }));
    expect(err).toMatchObject({ code: 'VALIDATION_ERROR', details: { fields: { 'shipping_address.phone': expect.any(String) } } });
  });

  test('cart flags unavailable lines and excludes them from the subtotal', () => {
    db.setCartItem(SEED.users.an, SEED.skus.teeM, 2);
    db.updateProduct(SEED.users.chi, SEED.products.tee, { status: 'archived' });
    const cart = db.cart(SEED.users.an);
    expect(cart.shops[0].items[0]).toMatchObject({ available: false });
    expect(cart.subtotal).toBe(0);
  });
});

describe('order lifecycle', () => {
  test('seller confirm → ship → deliver, buyer completes; history records each step', () => {
    const o = SEED.orders.pending;
    db.sellerAction(SEED.users.chi, o, 'confirm');
    db.sellerAction(SEED.users.chi, o, 'ship');
    db.sellerAction(SEED.users.chi, o, 'deliver', 'Giao tận tay');
    const done = db.buyerAction(SEED.users.an, o, 'complete');
    expect(done.status).toBe('COMPLETED');
    expect(done.history.map((h) => h.to)).toEqual(['PENDING', 'PENDING_CONFIRMATION', 'CONFIRMED', 'SHIPPING', 'DELIVERED', 'COMPLETED']);
    expect(done.history[4]).toMatchObject({ actor_role: 'seller', note: 'Giao tận tay' });
  });

  test('illegal moves → INVALID_STATE_TRANSITION; other parties → NOT_FOUND; cancel needs a reason', () => {
    const o = SEED.orders.pending;
    db.sellerAction(SEED.users.chi, o, 'confirm');
    expect(caught(() => db.buyerAction(SEED.users.an, o, 'cancel', 'Đổi ý'))).toMatchObject({ status: 409, code: 'INVALID_STATE_TRANSITION' });
    expect(caught(() => db.sellerAction(SEED.users.binh, o, 'ship'))).toMatchObject({ status: 404 });
    expect(caught(() => db.sellerAction(SEED.users.chi, o, 'cancel', ''))).toMatchObject({ code: 'VALIDATION_ERROR' });
  });

  test('cancelling returns stock', () => {
    const before = skuStock(SEED.products.tee, SEED.skus.teeM);
    const cancelled = db.buyerAction(SEED.users.an, SEED.orders.pending, 'cancel', 'Đặt nhầm size');
    expect(cancelled).toMatchObject({ status: 'CANCELLED', cancel_reason: 'Đặt nhầm size' });
    expect(skuStock(SEED.products.tee, SEED.skus.teeM)).toBe(before + 1);
  });

  test('buyer and seller lists are scoped, newest first and filterable by status', () => {
    expect(db.buyerOrders(SEED.users.an).items.map((o) => o.id)).toEqual([SEED.orders.pending, SEED.orders.delivered]);
    expect(db.buyerOrders(SEED.users.an, 'DELIVERED').items).toHaveLength(1);
    expect(db.sellerOrders(SEED.users.binh).items.map((o) => o.id)).toEqual([SEED.orders.delivered]);
    expect(db.sellerOrders(SEED.users.an).items).toEqual([]);
  });
});

test('state persists to the given storage', () => {
  const storage = window.localStorage;
  storage.clear();
  const first = new MockDb(storage);
  first.register({ email: 'persist@example.com', password: 'matkhau123', full_name: 'P' });
  expect(new MockDb(storage).state.users.some((u) => u.email === 'persist@example.com')).toBe(true);
  storage.clear();
});
