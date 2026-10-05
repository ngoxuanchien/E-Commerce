import { formatVnd } from '@/lib/format';
import type { PayoutStatus, WalletEntry } from '@/api/types';

export const ENTRY_KIND_LABEL: Record<WalletEntry['kind'], string> = {
  RELEASE: 'Tiền đơn hàng',
  PAYOUT_REQUEST: 'Rút tiền',
  PAYOUT_FAIL: 'Hoàn lại tiền rút',
};

export const PAYOUT_STATUS_LABEL: Record<PayoutStatus, string> = {
  REQUESTED: 'Đang xử lý',
  SETTLED: 'Đã chuyển',
  FAILED: 'Thất bại',
};

export function formatSignedVnd(n: number): string {
  if (n > 0) return '+' + formatVnd(n);
  if (n < 0) return '−' + formatVnd(Math.abs(n));
  return formatVnd(0);
}
