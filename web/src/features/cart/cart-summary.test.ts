import { describe, expect, test } from 'vitest';
import type { Cart, CartItem } from '@/api/types';
import { groupForCheckout, lineIssue, selectableShopIds, summarizeSelection } from './cart-summary';

const item = (sku: string, over: Partial<CartItem> = {}): CartItem => ({
  sku_id: sku, product_id: `p-${sku}`, product_name: `P ${sku}`, variant_name: 'V', image_url: null,
  unit_price: 10_000, quantity: 1, stock: 5, available: true, ...over,
});
const cart: Cart = {
  shops: [
    { shop: { id: 'A', name: 'Shop A', slug: 'a' }, items: [item('a1', { quantity: 2 }), item('a2', { available: false, unit_price: 99_000 })], shipping_fee: 30_000 },
    { shop: { id: 'B', name: 'Shop B', slug: 'b' }, items: [item('b1', { quantity: 9, stock: 3 })], shipping_fee: 25_000 },
    { shop: { id: 'C', name: 'Shop C', slug: 'c' }, items: [item('c1', { available: false })], shipping_fee: 30_000 },
  ],
  subtotal: 110_000,
};

describe('cart summary', () => {
  test('a shop with only unavailable lines cannot be selected', () => {
    expect([...selectableShopIds(cart)]).toEqual(['A', 'B']);
  });

  test('selection excludes unavailable lines but keeps over-stock lines for the server to judge', () => {
    expect(summarizeSelection(cart, new Set(['A', 'B', 'C']))).toEqual({ skuIds: ['a1', 'b1'], subtotal: 110_000, count: 11 });
    expect(summarizeSelection(cart, new Set(['A']))).toEqual({ skuIds: ['a1'], subtotal: 20_000, count: 2 });
    expect(summarizeSelection(cart, new Set())).toEqual({ skuIds: [], subtotal: 0, count: 0 });
  });

  test('lineIssue explains unavailable, shortage and over-stock lines', () => {
    expect(lineIssue(item('x', { available: false }), [])).toBe('Sản phẩm không còn bán');
    expect(lineIssue(item('x'), [{ sku_id: 'x', requested: 3, available: 1 }])).toBe('Chỉ còn 1 sản phẩm');
    expect(lineIssue(item('x'), [{ sku_id: 'x', requested: 3, available: 0 }])).toBe('Đã hết hàng');
    expect(lineIssue(item('x', { quantity: 9, stock: 3 }), [])).toBe('Chỉ còn 3 sản phẩm');
    expect(lineIssue(item('x'), [])).toBeNull();
  });

  test('groupForCheckout keeps only requested available lines, per shop, with subtotals and the shop\'s shipping fee', () => {
    expect(groupForCheckout(cart, ['a1', 'a2', 'b1', 'zz'])).toEqual([
      { shop: cart.shops[0].shop, items: [cart.shops[0].items[0]], subtotal: 20_000, shipping_fee: 30_000 },
      { shop: cart.shops[1].shop, items: [cart.shops[1].items[0]], subtotal: 90_000, shipping_fee: 25_000 },
    ]);
    expect(groupForCheckout(cart, ['c1'])).toEqual([]);
  });
});
