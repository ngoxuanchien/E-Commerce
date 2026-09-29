import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { type ProductQuery, qk } from '../query-keys';

export function useCategories() {
  return useQuery({ queryKey: qk.categories, queryFn: () => unwrap(api.GET('/api/categories')), staleTime: 5 * 60_000 });
}

export function useProductSearch(query: ProductQuery, limit = 20) {
  return useInfiniteQuery({
    queryKey: [...qk.products(query), limit],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/products', { params: { query: { ...query, limit, cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useProduct(id: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: qk.product(id),
    queryFn: () => unwrap(api.GET('/api/products/{id}', { params: { path: { id } } })),
    enabled: options.enabled ?? true,
  });
}

export function useShop(slug: string) {
  return useQuery({ queryKey: qk.shop(slug), queryFn: () => unwrap(api.GET('/api/shops/{slug}', { params: { path: { slug } } })) });
}
