import type { Shortage } from '@/api/errors';
import type { Cart, CartItem, ShopRef } from '@/api/types';

export function selectableShopIds(cart: Cart): Set<string> {
  return new Set(cart.shops.filter((s) => s.items.some((i) => i.available)).map((s) => s.shop.id));
}

/** Only available lines count; over-stock lines are still sent so the server can answer OUT_OF_STOCK. */
export function summarizeSelection(cart: Cart, selectedShopIds: ReadonlySet<string>) {
  const items = cart.shops.filter((s) => selectedShopIds.has(s.shop.id)).flatMap((s) => s.items.filter((i) => i.available));
  return {
    skuIds: items.map((i) => i.sku_id),
    subtotal: items.reduce((sum, i) => sum + i.unit_price * i.quantity, 0),
    count: items.reduce((sum, i) => sum + i.quantity, 0),
  };
}

export function lineIssue(item: CartItem, shortages: Shortage[]): string | null {
  if (!item.available) return 'Sản phẩm không còn bán';
  const shortage = shortages.find((s) => s.sku_id === item.sku_id);
  const remaining = shortage ? shortage.available : item.quantity > item.stock ? item.stock : null;
  if (remaining === null) return null;
  return remaining > 0 ? `Chỉ còn ${remaining} sản phẩm` : 'Đã hết hàng';
}

export function groupForCheckout(cart: Cart, skuIds: string[]): Array<{ shop: ShopRef; items: CartItem[]; subtotal: number }> {
  const wanted = new Set(skuIds);
  return cart.shops
    .map((s) => {
      const items = s.items.filter((i) => i.available && wanted.has(i.sku_id));
      return { shop: s.shop, items, subtotal: items.reduce((sum, i) => sum + i.unit_price * i.quantity, 0) };
    })
    .filter((g) => g.items.length > 0);
}
