import { useSearchParams } from 'react-router';
import { toast } from 'sonner';
import { useSellerOrderAction, useSellerOrders } from '@/api/hooks/orders';
import { errorMessage, isRetryable } from '@/api/errors';
import type { Order } from '@/api/types';
import { LoadMore } from '@/components/LoadMore';
import { StatusTabs } from '@/components/StatusTabs';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Button } from '@/components/ui/button';
import { CancelOrderDialog } from '@/features/orders/CancelOrderDialog';
import { OrderCard } from '@/features/orders/OrderCard';
import { ORDER_STATUS_TABS, type SellerAction, parseOrderStatus, sellerActionsFor } from '@/features/orders/order-status';

const ACTION_LABEL: Record<Exclude<SellerAction, 'cancel'>, string> = { confirm: 'Xác nhận đơn', ship: 'Bắt đầu giao', deliver: 'Xác nhận đã giao' };
const ACTION_DONE: Record<SellerAction, string> = {
  confirm: 'Đã xác nhận đơn',
  ship: 'Đơn đã chuyển sang Đang giao',
  deliver: 'Đơn đã được giao',
  cancel: 'Đã huỷ đơn',
};

export function SellerOrdersPage() {
  const [params, setParams] = useSearchParams();
  const status = parseOrderStatus(params.get('status'));
  const orders = useSellerOrders(status);
  const act = useSellerOrderAction();
  const items = orders.data?.pages.flatMap((p) => p.items) ?? [];

  const actionsFor = (order: Order) =>
    sellerActionsFor(order.status).map((action) =>
      action === 'cancel' ? (
        <CancelOrderDialog
          key={action}
          size="sm"
          triggerLabel="Huỷ đơn"
          pending={act.isPending}
          onConfirm={(reason) => act.mutateAsync({ id: order.id, action, reason }).then(() => toast.success(ACTION_DONE.cancel))}
        />
      ) : (
        <Button
          key={action}
          size="sm"
          disabled={act.isPending}
          onClick={() =>
            act.mutate(
              { id: order.id, action },
              {
                onSuccess: () => toast.success(ACTION_DONE[action]),
                onError: (err) => {
                  if (!isRetryable(err)) toast.error(errorMessage(err));
                },
              },
            )
          }
        >
          {ACTION_LABEL[action]}
        </Button>
      ),
    );

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">Đơn hàng</h1>
      <StatusTabs label="Lọc theo trạng thái" options={ORDER_STATUS_TABS} value={status} onChange={(s) => setParams(s ? { status: s } : {})} />
      {orders.isPending ? (
        <PageSpinner />
      ) : orders.isError ? (
        <ErrorState error={orders.error} onRetry={() => void orders.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="Chưa có đơn hàng nào ở trạng thái này." />
      ) : (
        <div className="space-y-4">
          {items.map((order) => (
            <OrderCard key={order.id} order={order} href={`/orders/${order.id}`} showBuyer actions={actionsFor(order)} />
          ))}
        </div>
      )}
      <LoadMore hasNextPage={orders.hasNextPage} isFetchingNextPage={orders.isFetchingNextPage} onLoadMore={() => void orders.fetchNextPage()} />
    </div>
  );
}
