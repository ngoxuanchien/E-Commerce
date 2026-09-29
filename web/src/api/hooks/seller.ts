import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import type { ShopCreate, ShopUpdate } from '../types';

export function useMyShop() {
  return useQuery({ queryKey: qk.myShop, queryFn: () => unwrap(api.GET('/api/shops/me')) });
}

export function useCreateShop() {
  return useMutation({ mutationFn: (body: ShopCreate) => unwrap(api.POST('/api/shops', { body })) });
}

export function useUpdateMyShop() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ShopUpdate) => unwrap(api.PATCH('/api/shops/me', { body })),
    onSuccess: (shop) => {
      queryClient.setQueryData(qk.myShop, shop);
      void queryClient.invalidateQueries({ queryKey: qk.shop(shop.slug) });
    },
  });
}
