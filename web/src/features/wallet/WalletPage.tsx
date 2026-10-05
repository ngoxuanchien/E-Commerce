import { Link } from 'react-router';
import { useWallet, useWalletEntries } from '@/api/hooks/wallet';
import { formatDateTime } from '@/lib/format';
import { shortId } from '@/features/orders/order-status';
import { LoadMore } from '@/components/LoadMore';
import { ErrorState, EmptyState, PageSpinner } from '@/components/states';
import { Price } from '@/components/Price';
import { cn } from '@/lib/utils';
import { ENTRY_KIND_LABEL, formatSignedVnd } from './wallet-labels';
import { PayoutForm } from './PayoutForm';

export function WalletPage() {
  const wallet = useWallet();
  const entries = useWalletEntries();
  const items = entries.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Ví người bán</h1>

      {wallet.isPending ? (
        <PageSpinner />
      ) : wallet.isError ? (
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      ) : (
        <>
          <div className="grid gap-4 md:grid-cols-3">
            <div className="rounded-lg border bg-card p-4" data-testid="wallet-available">
              <p className="text-sm text-muted-foreground">Số dư khả dụng</p>
              <Price value={wallet.data?.available} className="text-xl font-semibold" />
            </div>
            <div className="rounded-lg border bg-card p-4" data-testid="wallet-in-escrow">
              <p className="text-sm text-muted-foreground">Đang giữ</p>
              <Price value={wallet.data?.in_escrow} className="text-xl font-semibold" />
            </div>
            <div className="rounded-lg border bg-card p-4" data-testid="wallet-pending-payout">
              <p className="text-sm text-muted-foreground">Đang rút</p>
              <Price value={wallet.data?.pending_payout} className="text-xl font-semibold" />
            </div>
          </div>
          <PayoutForm available={wallet.data?.available ?? 0} />
        </>
      )}

      <h2 className="text-xl font-bold">Lịch sử giao dịch</h2>

      {entries.isPending ? (
        <PageSpinner />
      ) : entries.isError ? (
        <ErrorState error={entries.error} onRetry={() => void entries.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="Chưa có giao dịch nào." />
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-3 text-left font-medium">Thời gian</th>
                <th className="px-4 py-3 text-left font-medium">Loại</th>
                <th className="px-4 py-3 text-left font-medium">Đơn hàng</th>
                <th className="px-4 py-3 text-right font-medium">Số tiền</th>
              </tr>
            </thead>
            <tbody>
              {items.map((entry) => (
                <tr key={entry.id} data-testid="wallet-entry" className="border-b hover:bg-muted/50">
                  <td className="px-4 py-3">{formatDateTime(entry.created_at)}</td>
                  <td className="px-4 py-3">{ENTRY_KIND_LABEL[entry.kind]}</td>
                  <td className="px-4 py-3">
                    {entry.order_id ? (
                      <Link to={`/orders/${entry.order_id}`} className="text-tarp hover:underline">
                        {shortId(entry.order_id)}
                      </Link>
                    ) : (
                      '—'
                    )}
                  </td>
                  <td className={cn('px-4 py-3 text-right', entry.amount > 0 ? 'text-jade' : 'text-destructive')}>
                    {formatSignedVnd(entry.amount)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <LoadMore hasNextPage={entries.hasNextPage} isFetchingNextPage={entries.isFetchingNextPage} onLoadMore={() => void entries.fetchNextPage()} />
    </div>
  );
}
