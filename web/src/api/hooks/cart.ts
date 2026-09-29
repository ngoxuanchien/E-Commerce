import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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

/** PUT sets an absolute quantity, so "add" reads the current line first. */
export function useAddToCart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ skuId, quantity }: { skuId: string; quantity: number }) => {
      const cart = await queryClient.fetchQuery({ queryKey: qk.cart, queryFn: fetchCart });
      const existing = cart.shops.flatMap((s) => s.items).find((i) => i.sku_id === skuId)?.quantity ?? 0;
      return unwrap(api.PUT('/api/cart/items/{sku_id}', { params: { path: { sku_id: skuId } }, body: { quantity: Math.min(99, existing + quantity) } }));
    },
    onSuccess: (cart) => queryClient.setQueryData(qk.cart, cart),
  });
}
