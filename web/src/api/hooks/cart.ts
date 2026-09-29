import { useQuery } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import type { Cart } from '../types';

export const fetchCart = () => unwrap(api.GET('/api/cart'));

export function useCart(enabled = true) {
  return useQuery({ queryKey: qk.cart, queryFn: fetchCart, enabled });
}

export function cartItemCount(cart?: Cart): number {
  return cart?.shops.reduce((n, shop) => n + shop.items.reduce((m, item) => m + item.quantity, 0), 0) ?? 0;
}
