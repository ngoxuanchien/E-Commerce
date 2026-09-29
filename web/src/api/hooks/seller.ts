import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import type { ProductInput, ProductPatch, ProductStatus, ShopCreate, ShopUpdate } from '../types';

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

export function useSellerProducts(status?: ProductStatus) {
  return useInfiniteQuery({
    queryKey: qk.sellerProducts(status),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/seller/products', { params: { query: { status, cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

function useProductInvalidation() {
  const queryClient = useQueryClient();
  return (productId: string) => {
    void queryClient.invalidateQueries({ queryKey: ['seller-products'] });
    void queryClient.invalidateQueries({ queryKey: ['products'] });
    void queryClient.invalidateQueries({ queryKey: qk.product(productId) });
  };
}

export function useCreateSellerProduct() {
  const invalidate = useProductInvalidation();
  return useMutation({
    mutationFn: (body: ProductInput) => unwrap(api.POST('/api/seller/products', { body })),
    onSuccess: (p) => invalidate(p.id),
  });
}

export function useUpdateSellerProduct() {
  const invalidate = useProductInvalidation();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: ProductPatch }) =>
      unwrap(api.PATCH('/api/seller/products/{id}', { params: { path: { id } }, body: input })),
    onSuccess: (p) => invalidate(p.id),
  });
}

export function useArchiveSellerProduct() {
  const invalidate = useProductInvalidation();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE('/api/seller/products/{id}', { params: { path: { id } } })),
    onSuccess: (_data, id) => invalidate(id),
  });
}
