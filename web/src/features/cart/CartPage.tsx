import { useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { useCart, useRemoveCartItem, useSetCartQuantity } from '@/api/hooks/cart';
import { type Shortage, errorMessage, isRetryable } from '@/api/errors';
import type { CartItem, CartShop } from '@/api/types';
import { Awning } from '@/components/Awning';
import { Price } from '@/components/Price';
import { QuantityInput } from '@/components/QuantityInput';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { cn } from '@/lib/utils';
import { lineIssue, selectableShopIds, summarizeSelection } from './cart-summary';

const reportError = (err: unknown) => {
  if (!isRetryable(err)) toast.error(errorMessage(err));
};

export function CartPage() {
  const cart = useCart();
  const location = useLocation();
  const navigate = useNavigate();
  const setQuantity = useSetCartQuantity();
  const remove = useRemoveCartItem();
  const [deselected, setDeselected] = useState<ReadonlySet<string>>(new Set());
  const shortages = (location.state as { shortages?: Shortage[] } | null)?.shortages ?? [];

  if (cart.isPending) return <PageSpinner />;
  if (cart.isError) return <ErrorState error={cart.error} onRetry={() => void cart.refetch()} />;
  if (cart.data.shops.length === 0) {
    return (
      <EmptyState title="Giỏ hàng đang trống">
        <Link to="/" className="font-medium text-tarp hover:underline">Tiếp tục mua sắm</Link>
      </EmptyState>
    );
  }

  const selectable = selectableShopIds(cart.data);
  const selected = new Set([...selectable].filter((id) => !deselected.has(id)));
  const summary = summarizeSelection(cart.data, selected);
  const toggle = (shopId: string, checked: boolean) =>
    setDeselected((prev) => {
      const next = new Set(prev);
      if (checked) next.delete(shopId);
      else next.add(shopId);
      return next;
    });
  const goToCheckout = () => navigate(`/checkout?${new URLSearchParams(summary.skuIds.map((id) => ['sku', id]))}`);

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_320px] lg:items-start">
      <section aria-labelledby="cart-title" className="space-y-4">
        <h1 id="cart-title" className="text-3xl font-bold">Giỏ hàng</h1>
        {shortages.length > 0 ? (
          <Alert variant="destructive">
            <AlertDescription>Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.</AlertDescription>
          </Alert>
        ) : null}
        {cart.data.shops.map((group) => (
          <CartShopGroup
            key={group.shop.id}
            group={group}
            checked={selected.has(group.shop.id)}
            selectable={selectable.has(group.shop.id)}
            shortages={shortages}
            onToggle={(checked) => toggle(group.shop.id, checked)}
            onQuantity={(skuId, quantity) => setQuantity.mutate({ skuId, quantity }, { onError: reportError })}
            onRemove={(skuId) => remove.mutate(skuId, { onError: reportError })}
          />
        ))}
      </section>
      <aside aria-label="Tóm tắt" className="space-y-3 rounded-lg border bg-card p-5 lg:sticky lg:top-4">
        <div className="flex items-baseline justify-between gap-3">
          <span>Tạm tính ({summary.count} sản phẩm)</span>
          <Price value={summary.subtotal} className="text-2xl" />
        </div>
        <p className="text-sm text-muted-foreground">Phí vận chuyển tính theo từng shop ở bước thanh toán.</p>
        <Button size="lg" className="w-full" disabled={summary.skuIds.length === 0} onClick={goToCheckout}>
          Mua hàng
        </Button>
      </aside>
    </div>
  );
}

function CartShopGroup({ group, checked, selectable, shortages, onToggle, onQuantity, onRemove }: {
  group: CartShop;
  checked: boolean;
  selectable: boolean;
  shortages: Shortage[];
  onToggle: (checked: boolean) => void;
  onQuantity: (skuId: string, quantity: number) => void;
  onRemove: (skuId: string) => void;
}) {
  return (
    <section aria-label={`Shop ${group.shop.name}`} className="overflow-hidden rounded-lg border bg-card">
      <Awning />
      <header className="flex items-center gap-3 border-b px-4 py-3">
        <Checkbox
          checked={checked}
          disabled={!selectable}
          onCheckedChange={(value) => onToggle(value === true)}
          aria-label={`Chọn tất cả sản phẩm của ${group.shop.name}`}
        />
        <Link to={`/shops/${group.shop.slug}`} className="font-semibold hover:text-tarp">{group.shop.name}</Link>
      </header>
      <ul className="divide-y">
        {group.items.map((item) => (
          <CartLine key={item.sku_id} item={item} issue={lineIssue(item, shortages)} onQuantity={onQuantity} onRemove={onRemove} />
        ))}
      </ul>
    </section>
  );
}

function CartLine({ item, issue, onQuantity, onRemove }: {
  item: CartItem;
  issue: string | null;
  onQuantity: (skuId: string, quantity: number) => void;
  onRemove: (skuId: string) => void;
}) {
  const label = `${item.product_name} (${item.variant_name})`;
  return (
    <li className={cn('grid grid-cols-[64px_1fr] gap-3 p-4 sm:grid-cols-[80px_1fr_auto]', !item.available && 'bg-muted/40')}>
      <div className="size-16 overflow-hidden rounded-md border bg-muted sm:size-20">
        {item.image_url ? <img src={item.image_url} alt="" className="size-full object-cover" /> : null}
      </div>
      <div className="min-w-0 space-y-1">
        <Link to={`/products/${item.product_id}`} className="line-clamp-2 font-medium hover:text-tarp">{item.product_name}</Link>
        <p className="text-sm text-muted-foreground">{item.variant_name}</p>
        <Price value={item.unit_price} className="text-base" />
        {issue ? <p className="text-sm font-medium text-chili">{issue}</p> : null}
      </div>
      <div className="col-span-2 flex items-center justify-between gap-3 sm:col-span-1 sm:flex-col sm:items-end">
        <QuantityInput
          id={`qty-${item.sku_id}`}
          label={`Số lượng ${label}`}
          value={item.quantity}
          max={99}
          disabled={!item.available}
          onChange={(quantity) => onQuantity(item.sku_id, quantity)}
        />
        <Price value={item.unit_price * item.quantity} className="text-lg" />
        <Button variant="ghost" size="sm" aria-label={`Xoá ${label}`} onClick={() => onRemove(item.sku_id)}>
          Xoá
        </Button>
      </div>
    </li>
  );
}
