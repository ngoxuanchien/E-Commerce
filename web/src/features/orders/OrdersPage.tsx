import { Link, useSearchParams } from 'react-router';
import { useMyOrders } from '@/api/hooks/orders';
import { LoadMore } from '@/components/LoadMore';
import { StatusTabs } from '@/components/StatusTabs';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { OrderCard } from './OrderCard';
import { ORDER_STATUS_TABS, parseOrderStatus } from './order-status';

export function OrdersPage() {
  const [params, setParams] = useSearchParams();
  const status = parseOrderStatus(params.get('status'));
  const placed = Number(params.get('placed') ?? 0);
  const orders = useMyOrders(status);
  const items = orders.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <div className="space-y-4">
      <h1 className="text-3xl font-bold">Đơn mua</h1>
      {placed > 0 ? (
        <Alert>
          <AlertDescription>Đặt hàng thành công. Đã tạo {placed} đơn hàng, mỗi shop một đơn.</AlertDescription>
        </Alert>
      ) : null}
      <StatusTabs label="Lọc theo trạng thái" options={ORDER_STATUS_TABS} value={status} onChange={(s) => setParams(s ? { status: s } : {})} />
      {orders.isPending ? (
        <PageSpinner />
      ) : orders.isError ? (
        <ErrorState error={orders.error} onRetry={() => void orders.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="Chưa có đơn hàng nào ở trạng thái này.">
          <Link to="/" className="font-medium text-tarp hover:underline">Tiếp tục mua sắm</Link>
        </EmptyState>
      ) : (
        <div className="space-y-4">
          {items.map((order) => (
            <OrderCard key={order.id} order={order} href={`/orders/${order.id}`} />
          ))}
        </div>
      )}
      <LoadMore hasNextPage={orders.hasNextPage} isFetchingNextPage={orders.isFetchingNextPage} onLoadMore={() => void orders.fetchNextPage()} />
    </div>
  );
}
