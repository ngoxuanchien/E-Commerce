import { Link, useParams } from 'react-router';
import { toast } from 'sonner';
import { useCancelMyOrder, useCompleteMyOrder, useOrder } from '@/api/hooks/orders';
import { ApiError, errorMessage, isRetryable } from '@/api/errors';
import { Awning } from '@/components/Awning';
import { Price } from '@/components/Price';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useAuth } from '@/features/auth/auth-context';
import { formatDateTime } from '@/lib/format';
import { PayAgainButton } from '@/features/payment/PayAgainButton';
import { CancelOrderDialog } from './CancelOrderDialog';
import { OrderStatusBadge } from './OrderStatusBadge';
import { PaymentStatusBadge } from './PaymentStatusBadge';
import { PaymentCountdown } from './PaymentCountdown';
import { OrderTimeline } from './OrderTimeline';
import { buyerCanCancel, buyerCanComplete, shortId } from './order-status';
import { isPayable, PAYMENT_METHOD_LABEL } from './payment-labels';

const reportError = (err: unknown) => {
  if (!isRetryable(err)) toast.error(errorMessage(err));
};

export function OrderDetailPage() {
  const { id = '' } = useParams();
  const order = useOrder(id);
  const { user } = useAuth();
  const cancel = useCancelMyOrder();
  const complete = useCompleteMyOrder();

  if (order.isPending) return <PageSpinner />;
  if (order.isError) {
    if (order.error instanceof ApiError && order.error.code === 'NOT_FOUND') {
      return (
        <EmptyState title="Không tìm thấy đơn hàng">
          <Link to="/orders" className="font-medium text-tarp hover:underline">Xem đơn mua</Link>
        </EmptyState>
      );
    }
    return <ErrorState error={order.error} onRetry={() => void order.refetch()} />;
  }
  const o = order.data;
  const a = o.shipping_address;
  const isBuyer = user?.id === o.buyer_id;

  return (
    <div className="space-y-6">
      <Link to="/orders" className="text-sm font-medium text-tarp hover:underline">Quay lại đơn mua</Link>
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">Đơn hàng {shortId(o.id)}</h1>
          <p className="text-sm text-muted-foreground">Đặt lúc {formatDateTime(o.created_at)}</p>
        </div>
        <OrderStatusBadge status={o.status} />
      </header>
      {o.status === 'CANCELLED' && o.cancel_reason ? (
        <Alert>
          <AlertDescription>Lý do huỷ: {o.cancel_reason}</AlertDescription>
        </Alert>
      ) : null}
      <div className="grid gap-6 lg:grid-cols-[1fr_320px] lg:items-start">
        <div className="space-y-6">
          <section aria-labelledby="items-title" className="overflow-hidden rounded-lg border bg-card">
            <Awning />
            <div className="p-4">
              <h2 id="items-title" className="mb-2 font-semibold">{o.shop.name}</h2>
              <ul className="divide-y">
                {o.items.map((item) => (
                  <li key={item.sku_id} className="flex items-center gap-3 py-3">
                    <div className="size-14 shrink-0 overflow-hidden rounded-md border bg-muted">
                      {item.image_url ? <img src={item.image_url} alt="" className="size-full object-cover" /> : null}
                    </div>
                    <div className="min-w-0 flex-1 text-sm">
                      <Link to={`/products/${item.product_id}`} className="font-medium hover:text-tarp">{item.product_name}</Link>
                      <p className="text-muted-foreground">{item.variant_name}, số lượng {item.quantity}</p>
                    </div>
                    <Price value={item.line_total} />
                  </li>
                ))}
              </ul>
            </div>
          </section>
          <section aria-labelledby="history-title" className="rounded-lg border bg-card p-4">
            <h2 id="history-title" className="mb-3 font-semibold">Lịch sử đơn hàng</h2>
            <OrderTimeline history={o.history} />
          </section>
        </div>
        <aside aria-label="Thông tin đơn hàng" className="space-y-4">
          <section aria-labelledby="address-title" className="space-y-1 rounded-lg border bg-card p-4 text-sm">
            <h2 id="address-title" className="mb-1 font-semibold">Địa chỉ nhận hàng</h2>
            <p className="font-medium">{a.full_name}</p>
            <p>{a.phone}</p>
            <p>{[a.line1, a.ward, a.district, a.province].join(', ')}</p>
          </section>
          <section aria-labelledby="payment-title" className="rounded-lg border bg-card p-4 text-sm">
            <h2 id="payment-title" className="mb-2 font-semibold">Thanh toán</h2>
            <dl className="space-y-1">
              <div className="flex justify-between"><dt>Tạm tính</dt><dd><Price value={o.subtotal} /></dd></div>
              <div className="flex justify-between"><dt>Phí vận chuyển</dt><dd><Price value={o.shipping_fee} /></dd></div>
              <div className="flex items-baseline justify-between border-t pt-2"><dt>Thành tiền</dt><dd><Price value={o.total} className="text-2xl" /></dd></div>
            </dl>
            <p className="mt-3 text-muted-foreground">{PAYMENT_METHOD_LABEL[o.payment_method]}</p>
            <div className="mt-2">
              <PaymentStatusBadge status={o.payment_status} />
            </div>
            {o.status === 'AWAITING_PAYMENT' && o.payment_deadline ? (
              <div className="mt-2">
                <PaymentCountdown deadline={o.payment_deadline} />
              </div>
            ) : null}
            {!isBuyer && o.payment_method !== 'COD' ? (
              <dl className="space-y-1 border-t pt-2 mt-2">
                <div className="flex justify-between"><dt>Phí sàn ({o.commission_rate_bps / 100}%)</dt><dd><Price value={o.commission_amount} /></dd></div>
                <div className="flex justify-between"><dt>Bạn nhận được</dt><dd><Price value={o.total - o.commission_amount} /></dd></div>
              </dl>
            ) : null}
          </section>
          {isBuyer && isPayable(o) ? (
            <PayAgainButton order={o} className="w-full" />
          ) : null}
          {isBuyer && buyerCanComplete(o.status) ? (
            <Button className="w-full" disabled={complete.isPending} onClick={() => complete.mutate(o.id, { onError: reportError })}>
              Đã nhận được hàng
            </Button>
          ) : null}
          {isBuyer && buyerCanCancel(o.status) ? (
            <CancelOrderDialog triggerLabel="Huỷ đơn hàng" pending={cancel.isPending} onConfirm={(reason) => cancel.mutateAsync({ id: o.id, reason })} />
          ) : null}
        </aside>
      </div>
    </div>
  );
}
