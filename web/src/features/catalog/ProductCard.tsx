import { Link } from 'react-router';
import type { ProductSummary } from '@/api/types';
import { Price } from '@/components/Price';

export function ProductCard({ product }: { product: ProductSummary }) {
  return (
    <article className="relative flex h-full flex-col overflow-hidden rounded-lg border bg-card has-[a:focus-visible]:ring-2 has-[a:focus-visible]:ring-ring">
      <div className="aspect-square bg-muted">
        {product.image_url ? (
          <img src={product.image_url} alt="" loading="lazy" className="size-full object-cover" />
        ) : (
          <div className="grid size-full place-items-center text-sm text-muted-foreground">Chưa có ảnh</div>
        )}
      </div>
      <div className="flex flex-1 flex-col gap-1 p-3">
        <h3 className="line-clamp-2 font-sans text-sm font-medium tracking-normal">
          <Link to={`/products/${product.id}`} className="after:absolute after:inset-0 focus-visible:outline-none">
            {product.name}
          </Link>
        </h3>
        <Price value={product.min_price} className="mt-auto text-xl" />
        <p className="truncate text-xs text-muted-foreground">{product.shop.name}</p>
      </div>
    </article>
  );
}
