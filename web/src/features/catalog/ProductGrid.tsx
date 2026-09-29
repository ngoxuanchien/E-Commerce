import type { ProductSummary } from '@/api/types';
import { EmptyState } from '@/components/states';
import { ProductCard } from './ProductCard';

export function ProductGrid({ products, empty = 'Chưa có sản phẩm nào.' }: { products: ProductSummary[]; empty?: string }) {
  if (products.length === 0) return <EmptyState title={empty} />;
  return (
    <ul aria-label="Sản phẩm" className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
      {products.map((product) => (
        <li key={product.id}>
          <ProductCard product={product} />
        </li>
      ))}
    </ul>
  );
}
