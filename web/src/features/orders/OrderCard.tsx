import type { ReactNode } from 'react';
import { Link } from 'react-router';
import type { Order } from '@/api/types';
import { Awning } from '@/components/Awning';
import { Price } from '@/components/Price';
import { Button } from '@/components/ui/button';
import { formatDateTime } from '@/lib/format';
import { OrderStatusBadge } from './OrderStatusBadge';
import { shortId } from './order-status';

export function OrderCard({ order, href, actions, showBuyer }: { order: Order; href: string; actions?: ReactNode; showBuyer?: boolean }) {
  const [first, ...rest] = order.items;
  return (
    <article aria-label={`Đơn hàng của ${order.shop.name}`} className="overflow-hidden rounded-lg border bg-card">
      <Awning />
      <header className="flex flex-wrap items-start justify-between gap-2 border-b px-4 py-3">
        <div>
          <p className="font-semibold">{order.shop.name}</p>
          <p className="text-xs text-muted-foreground">Mã đơn {shortId(order.id)}</p>
          <p className="text-xs text-muted-foreground">{formatDateTime(order.created_at)}</p>
        </div>
        <OrderStatusBadge status={order.status} />
      </header>
      {first ? (
        <div className="flex gap-3 p-4">
          <div className="size-14 shrink-0 overflow-hidden rounded-md border bg-muted">
            {first.image_url ? <img src={first.image_url} alt="" className="size-full object-cover" /> : null}
          </div>
          <div className="min-w-0 text-sm">
            <p className="line-clamp-1 font-medium">{first.product_name}</p>
            <p className="text-muted-foreground">{first.variant_name}, số lượng {first.quantity}</p>
            {rest.length > 0 ? <p className="text-muted-foreground">và {rest.length} sản phẩm khác</p> : null}
          </div>
        </div>
      ) : null}
      {showBuyer ? (
        <p className="px-4 pb-3 text-sm">Người nhận: {order.shipping_address.full_name}, {order.shipping_address.phone}</p>
      ) : null}
      <footer className="flex flex-wrap items-center justify-between gap-3 border-t px-4 py-3">
        <p className="flex items-baseline gap-2 text-sm">
          Thành tiền <Price value={order.total} className="text-xl" />
        </p>
        <div className="flex flex-wrap gap-2">
          {actions}
          <Button variant="outline" size="sm" asChild>
            <Link to={href}>Xem chi tiết</Link>
          </Button>
        </div>
      </footer>
    </article>
  );
}
