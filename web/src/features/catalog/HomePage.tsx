import { Link } from 'react-router';
import { useCategories, useProductSearch } from '@/api/hooks/catalog';
import { LoadMore } from '@/components/LoadMore';
import { ErrorState, PageSpinner } from '@/components/states';
import { ProductGrid } from './ProductGrid';

export function HomePage() {
  const categories = useCategories();
  const products = useProductSearch({ sort: 'newest' });
  return (
    <div className="space-y-10">
      <section aria-labelledby="home-title" className="space-y-2 pt-2">
        <h1 id="home-title" className="max-w-xl text-3xl font-bold sm:text-4xl">Nhiều sạp hàng, một giỏ hàng.</h1>
        <p className="max-w-xl text-muted-foreground">Chọn hàng từ nhiều shop và đặt một lần. Mỗi shop giao riêng, bạn trả tiền khi nhận hàng.</p>
      </section>
      <nav aria-label="Danh mục">
        <ul className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1">
          {categories.data?.map((c) => (
            <li key={c.id} className="shrink-0">
              <Link to={`/search?category=${c.slug}`} className="inline-flex rounded-full border bg-card px-4 py-2 text-sm font-medium hover:border-tarp hover:text-tarp">
                {c.name}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <section aria-labelledby="newest-title" className="space-y-4">
        <h2 id="newest-title" className="text-2xl font-bold">Mới lên sạp</h2>
        {products.isPending ? (
          <PageSpinner />
        ) : products.isError ? (
          <ErrorState error={products.error} onRetry={() => void products.refetch()} />
        ) : (
          <>
            <ProductGrid products={products.data.pages.flatMap((p) => p.items)} />
            <LoadMore hasNextPage={products.hasNextPage} isFetchingNextPage={products.isFetchingNextPage} onLoadMore={() => void products.fetchNextPage()} />
          </>
        )}
      </section>
    </div>
  );
}
