import { Link, useParams } from 'react-router';
import { useProductSearch, useShop } from '@/api/hooks/catalog';
import { ApiError } from '@/api/errors';
import { Awning } from '@/components/Awning';
import { LoadMore } from '@/components/LoadMore';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { ProductGrid } from '@/features/catalog/ProductGrid';
import { formatDate } from '@/lib/format';

export function ShopPage() {
  const { slug = '' } = useParams();
  const shop = useShop(slug);
  const products = useProductSearch({ shop: slug, sort: 'newest' });

  if (shop.isPending) return <PageSpinner />;
  if (shop.isError) {
    if (shop.error instanceof ApiError && shop.error.code === 'NOT_FOUND') {
      return (
        <EmptyState title="Shop không tồn tại">
          <Link to="/" className="font-medium text-tarp hover:underline">Về trang chủ</Link>
        </EmptyState>
      );
    }
    return <ErrorState error={shop.error} onRetry={() => void shop.refetch()} />;
  }

  return (
    <div className="space-y-6">
      <header className="overflow-hidden rounded-lg border bg-card">
        <Awning />
        <div className="space-y-1 p-5">
          <h1 className="text-3xl font-bold">{shop.data.name}</h1>
          {shop.data.description ? <p className="max-w-2xl text-muted-foreground">{shop.data.description}</p> : null}
          <p className="text-sm text-muted-foreground">Tham gia từ {formatDate(shop.data.created_at)}</p>
        </div>
      </header>
      <section aria-labelledby="shop-products-title" className="space-y-4">
        <h2 id="shop-products-title" className="text-xl font-semibold">Sản phẩm của shop</h2>
        {products.isPending ? (
          <PageSpinner />
        ) : products.isError ? (
          <ErrorState error={products.error} onRetry={() => void products.refetch()} />
        ) : (
          <>
            <ProductGrid products={products.data.pages.flatMap((p) => p.items)} empty="Shop chưa có sản phẩm nào đang bán." />
            <LoadMore hasNextPage={products.hasNextPage} isFetchingNextPage={products.isFetchingNextPage} onLoadMore={() => void products.fetchNextPage()} />
          </>
        )}
      </section>
    </div>
  );
}
