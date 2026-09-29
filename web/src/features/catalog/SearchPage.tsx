import type { FormEvent } from 'react';
import { useSearchParams } from 'react-router';
import { useCategories, useProductSearch } from '@/api/hooks/catalog';
import { LoadMore } from '@/components/LoadMore';
import { ErrorState, PageSpinner } from '@/components/states';
import { Button } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { NativeSelect } from '@/components/ui/native-select';
import { ProductGrid } from './ProductGrid';
import { SORT_OPTIONS, parseProductQuery } from './search-params';

const FILTER_KEYS = ['category', 'min_price', 'max_price', 'sort'] as const;

export function SearchPage() {
  const [params, setParams] = useSearchParams();
  const query = parseProductQuery(params);
  const categories = useCategories();
  const results = useProductSearch(query);

  const onFilter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const next = new URLSearchParams();
    if (query.q) next.set('q', query.q);
    for (const key of FILTER_KEYS) {
      const value = String(data.get(key) ?? '').trim();
      if (value) next.set(key, value);
    }
    setParams(next);
  };

  return (
    <div className="grid gap-6 lg:grid-cols-[240px_1fr] lg:items-start">
      <details className="rounded-lg border bg-card">
        <summary className="cursor-pointer px-4 py-3 font-medium">Bộ lọc và sắp xếp</summary>
        <form key={params.toString()} onSubmit={onFilter} className="grid gap-4 border-t p-4">
          <Field id="category" label="Danh mục">
            <NativeSelect id="category" name="category" defaultValue={query.category ?? ''}>
              <option value="">Tất cả danh mục</option>
              {categories.data?.map((c) => (
                <optgroup key={c.id} label={c.name}>
                  <option value={c.slug}>Tất cả {c.name}</option>
                  {c.children.map((child) => (
                    <option key={child.id} value={child.slug}>{child.name}</option>
                  ))}
                </optgroup>
              ))}
            </NativeSelect>
          </Field>
          <div className="grid grid-cols-2 gap-2">
            <Field id="min_price" label="Giá từ (₫)">
              <Input id="min_price" name="min_price" inputMode="numeric" pattern="[0-9]*" defaultValue={query.min_price ?? ''} />
            </Field>
            <Field id="max_price" label="Đến (₫)">
              <Input id="max_price" name="max_price" inputMode="numeric" pattern="[0-9]*" defaultValue={query.max_price ?? ''} />
            </Field>
          </div>
          <Field id="sort" label="Sắp xếp">
            <NativeSelect id="sort" name="sort" defaultValue={query.sort ?? ''}>
              <option value="">Mặc định</option>
              {SORT_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </NativeSelect>
          </Field>
          <Button type="submit">Áp dụng</Button>
        </form>
      </details>
      <section aria-labelledby="results-title" className="space-y-4">
        <h1 id="results-title" className="text-2xl font-bold">{query.q ? `Kết quả cho “${query.q}”` : 'Tất cả sản phẩm'}</h1>
        {results.isPending ? (
          <PageSpinner />
        ) : results.isError ? (
          <ErrorState error={results.error} onRetry={() => void results.refetch()} />
        ) : (
          <>
            <ProductGrid products={results.data.pages.flatMap((p) => p.items)} empty="Không tìm thấy sản phẩm phù hợp. Thử từ khoá khác hoặc bỏ bớt bộ lọc." />
            <LoadMore hasNextPage={results.hasNextPage} isFetchingNextPage={results.isFetchingNextPage} onLoadMore={() => void results.fetchNextPage()} />
          </>
        )}
      </section>
    </div>
  );
}
