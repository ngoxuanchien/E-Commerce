import { useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router';
import { toast } from 'sonner';
import { useProduct } from '@/api/hooks/catalog';
import { useAddToCart } from '@/api/hooks/cart';
import { ApiError, errorMessage, isRetryable } from '@/api/errors';
import type { Product } from '@/api/types';
import { Awning } from '@/components/Awning';
import { Price } from '@/components/Price';
import { QuantityInput } from '@/components/QuantityInput';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Button } from '@/components/ui/button';
import { useAuth } from '@/features/auth/auth-context';
import { cn } from '@/lib/utils';
import { VariantSelector } from './VariantSelector';

export function ProductPage() {
  const { id = '' } = useParams();
  const product = useProduct(id);
  if (product.isPending) return <PageSpinner />;
  if (product.isError) {
    if (product.error instanceof ApiError && product.error.code === 'NOT_FOUND') {
      return (
        <EmptyState title="Sản phẩm không tồn tại hoặc đã ngừng bán">
          <Link to="/" className="font-medium text-tarp hover:underline">Về trang chủ</Link>
        </EmptyState>
      );
    }
    return <ErrorState error={product.error} onRetry={() => void product.refetch()} />;
  }
  return <ProductView key={product.data.id} product={product.data} />;
}

function ProductView({ product }: { product: Product }) {
  const initial = product.skus.find((s) => s.active && s.stock > 0) ?? product.skus.find((s) => s.active);
  const [skuId, setSkuId] = useState(initial?.id);
  const [quantity, setQuantity] = useState(1);
  const [imageIndex, setImageIndex] = useState(0);
  const { user } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const addToCart = useAddToCart();
  const sku = product.skus.find((s) => s.id === skuId);
  const canBuy = !!sku && sku.stock > 0;
  const maxQuantity = Math.max(1, Math.min(99, sku?.stock ?? 1));
  const image = product.images[imageIndex];

  const onAdd = () => {
    if (!user) {
      navigate(`/login?next=${encodeURIComponent(location.pathname)}`);
      return;
    }
    if (!sku) return;
    addToCart.mutate(
      { skuId: sku.id, quantity: Math.min(quantity, maxQuantity) },
      {
        onSuccess: () => toast.success('Đã thêm vào giỏ hàng', { action: { label: 'Xem giỏ hàng', onClick: () => navigate('/cart') } }),
        onError: (err) => {
          if (isRetryable(err)) return;
          toast.error(errorMessage(err, { CONFLICT: 'Không thể thêm sản phẩm này vào giỏ. Bạn không thể mua sản phẩm của chính shop mình.' }));
        },
      },
    );
  };

  return (
    <article className="grid gap-8 md:grid-cols-2" aria-labelledby="product-title">
      <div className="space-y-3">
        <div className="aspect-square overflow-hidden rounded-lg border bg-card">
          {image ? (
            <img src={image.url} alt={product.name} className="size-full object-cover" />
          ) : (
            <div className="grid size-full place-items-center text-muted-foreground">Chưa có ảnh</div>
          )}
        </div>
        {product.images.length > 1 ? (
          <div className="flex gap-2 overflow-x-auto">
            {product.images.map((img, i) => (
              <button
                key={img.object_key}
                type="button"
                aria-label={`Xem ảnh ${i + 1}`}
                aria-pressed={i === imageIndex}
                onClick={() => setImageIndex(i)}
                className={cn('size-16 shrink-0 overflow-hidden rounded-md border-2', i === imageIndex ? 'border-tarp' : 'border-transparent')}
              >
                <img src={img.url} alt="" className="size-full object-cover" />
              </button>
            ))}
          </div>
        ) : null}
      </div>

      <div className="space-y-5">
        <h1 id="product-title" className="text-2xl font-bold sm:text-3xl">{product.name}</h1>
        <Price value={sku?.price ?? product.min_price} className="text-4xl" />
        <VariantSelector
          skus={product.skus}
          value={skuId}
          onChange={(id) => {
            setSkuId(id);
            setQuantity(1);
          }}
        />
        <p className={cn('text-sm font-medium', canBuy ? 'text-jade' : 'text-chili')}>
          {!sku ? 'Sản phẩm tạm ngừng bán' : sku.stock > 0 ? `Còn ${sku.stock} sản phẩm` : 'Hết hàng'}
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <QuantityInput id="quantity" value={quantity} max={maxQuantity} onChange={setQuantity} disabled={!canBuy} />
          <Button size="lg" onClick={onAdd} disabled={!canBuy || addToCart.isPending}>Thêm vào giỏ hàng</Button>
        </div>
        <section aria-label="Thông tin shop" className="overflow-hidden rounded-lg border bg-card">
          <Awning />
          <div className="flex items-center justify-between gap-3 p-4">
            <div>
              <p className="text-sm text-muted-foreground">Shop</p>
              <p className="font-semibold">{product.shop.name}</p>
            </div>
            <Button variant="outline" asChild>
              <Link to={`/shops/${product.shop.slug}`}>Xem shop</Link>
            </Button>
          </div>
        </section>
        {product.description ? (
          <section aria-labelledby="description-title">
            <h2 id="description-title" className="mb-2 text-lg font-semibold">Mô tả sản phẩm</h2>
            <p className="max-w-prose whitespace-pre-line text-sm leading-relaxed">{product.description}</p>
          </section>
        ) : null}
      </div>
    </article>
  );
}
