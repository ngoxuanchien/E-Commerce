import type { OrderHistoryEntry } from '@/api/types';
import { formatDateTime } from '@/lib/format';
import { ACTOR_LABEL, ORDER_STATUS_LABEL } from './order-status';

/** The history is a real sequence, so an ordered list with step markers fits here. */
export function OrderTimeline({ history }: { history: OrderHistoryEntry[] }) {
  return (
    <ol aria-label="Lịch sử đơn hàng" className="space-y-4 border-l-2 border-border pl-5">
      {history.map((entry, i) => (
        <li key={`${entry.to}-${i}`} className="relative">
          <span aria-hidden="true" className="absolute -left-[27px] top-1.5 size-3 rounded-full border-2 border-card bg-tarp" />
          <p className="font-medium">{ORDER_STATUS_LABEL[entry.to]}</p>
          <span className="block text-sm text-muted-foreground">{ACTOR_LABEL[entry.actor_role]}, {formatDateTime(entry.created_at)}</span>
          {entry.note ? <span className="block text-sm">Ghi chú: {entry.note}</span> : null}
        </li>
      ))}
    </ol>
  );
}
