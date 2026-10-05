import { Link, useSearchParams } from 'react-router';
import { usePayouts } from '@/api/hooks/wallet';
import type { PayoutStatus } from '@/api/types';
import { formatDateTime } from '@/lib/format';
import { StatusTabs } from '@/components/StatusTabs';
import { LoadMore } from '@/components/LoadMore';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Price } from '@/components/Price';
import { PAYOUT_STATUS_LABEL } from './wallet-labels';

const STATUS_OPTIONS: ReadonlyArray<{ value: PayoutStatus | undefined; label: string }> = [
  { value: undefined, label: 'Tất cả' },
  { value: 'REQUESTED', label: 'Đang xử lý' },
  { value: 'SETTLED', label: 'Đã chuyển' },
  { value: 'FAILED', label: 'Thất bại' },
];

export function PayoutsPage() {
  const [params, setParams] = useSearchParams();
  const statusParam = params.get('status');

  // Safe parsing: unknown value → undefined (show all)
  const status = STATUS_OPTIONS.some((opt) => opt.value === statusParam) ? (statusParam as PayoutStatus | undefined) : undefined;

  const payouts = usePayouts(status);
  const items = payouts.data?.pages.flatMap((p) => p.items) ?? [];

  const handleStatusChange = (newStatus: PayoutStatus | undefined) => {
    if (newStatus === undefined) {
      setParams({}); // Remove status param
    } else {
      setParams({ status: newStatus });
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Rút tiền</h1>
        <Link to="/seller/wallet" className="text-sm text-tarp hover:underline">
          Về ví
        </Link>
      </div>

      <StatusTabs label="Lọc theo trạng thái" options={STATUS_OPTIONS} value={status} onChange={handleStatusChange} />

      {payouts.isPending ? (
        <PageSpinner />
      ) : payouts.isError ? (
        <ErrorState error={payouts.error} onRetry={() => void payouts.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="Chưa có yêu cầu rút tiền nào." />
      ) : (
        <div className="space-y-3">
          {items.map((payout) => (
            <div
              key={payout.id}
              data-testid="payout-row"
              data-status={payout.status}
              className="flex items-center justify-between rounded-lg border bg-card p-4"
            >
              <div className="flex-1">
                <p className="text-sm text-muted-foreground">{formatDateTime(payout.requested_at)}</p>
                <p className="font-semibold">{PAYOUT_STATUS_LABEL[payout.status]}</p>
                {payout.settled_at ? (
                  <p className="text-xs text-muted-foreground">Chuyển vào: {formatDateTime(payout.settled_at)}</p>
                ) : payout.failure_reason ? (
                  <p className="text-xs text-destructive">Lý do: {payout.failure_reason}</p>
                ) : null}
              </div>
              <Price value={payout.amount} className="text-lg font-semibold" />
            </div>
          ))}
        </div>
      )}

      <LoadMore hasNextPage={payouts.hasNextPage} isFetchingNextPage={payouts.isFetchingNextPage} onLoadMore={() => void payouts.fetchNextPage()} />
    </div>
  );
}
