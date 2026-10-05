import type { OrderPaymentStatus } from '@/api/types';
import { cn } from '@/lib/utils';
import { PAYMENT_STATUS_LABEL } from './payment-labels';

const PAYMENT_STATUS_TONE: Record<Exclude<OrderPaymentStatus, 'NONE'>, string> = {
  PAID: 'bg-jade/10 text-jade',
  UNPAID: 'bg-turmeric/10 text-turmeric',
  REFUND_PENDING: 'bg-muted text-muted-foreground',
  REFUNDED: 'bg-muted text-muted-foreground',
};

export function PaymentStatusBadge({ status }: { status: OrderPaymentStatus }) {
  if (status === 'NONE') return null;

  return (
    <span
      data-testid="order-payment-status"
      data-payment-status={status}
      className={cn('inline-flex rounded-full px-2.5 py-0.5 text-xs font-semibold', PAYMENT_STATUS_TONE[status])}
    >
      {PAYMENT_STATUS_LABEL[status]}
    </span>
  );
}
