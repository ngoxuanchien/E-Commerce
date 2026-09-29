import type { ProductQuery } from '@/api/query-keys';

export const SORT_OPTIONS = [
  { value: 'relevance', label: 'Liên quan nhất' },
  { value: 'newest', label: 'Mới nhất' },
  { value: 'price_asc', label: 'Giá thấp đến cao' },
  { value: 'price_desc', label: 'Giá cao đến thấp' },
] as const satisfies ReadonlyArray<{ value: NonNullable<ProductQuery['sort']>; label: string }>;

const toInt = (value: string | null) => (value && /^\d+$/.test(value) ? Number(value) : undefined);

export function parseProductQuery(params: URLSearchParams): ProductQuery {
  const sort = params.get('sort');
  return {
    q: params.get('q')?.trim() || undefined,
    category: params.get('category') || undefined,
    min_price: toInt(params.get('min_price')),
    max_price: toInt(params.get('max_price')),
    sort: SORT_OPTIONS.find((o) => o.value === sort)?.value,
  };
}
