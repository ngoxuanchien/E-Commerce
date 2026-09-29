import type { OrderStatus } from '@/api/types';
import { cn } from '@/lib/utils';
import { ORDER_STATUS_LABEL, ORDER_STATUS_TONE } from './order-status';

export function OrderStatusBadge({ status }: { status: OrderStatus }) {
  return (
    <span data-status={status} className={cn('inline-flex rounded-full px-2.5 py-0.5 text-xs font-semibold', ORDER_STATUS_TONE[status])}>
      {ORDER_STATUS_LABEL[status]}
    </span>
  );
}
