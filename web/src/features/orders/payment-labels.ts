import type { Order, OrderPaymentStatus, PaymentMethod } from '@/api/types';

export const PAYMENT_METHOD_LABEL: Record<PaymentMethod, string> = {
  COD: 'Thanh toán khi nhận hàng (COD)',
  VNPAY: 'VNPay',
  STRIPE: 'Thẻ quốc tế (Stripe)',
};

export const PAYMENT_STATUS_LABEL: Record<Exclude<OrderPaymentStatus, 'NONE'>, string> = {
  UNPAID: 'Chưa thanh toán',
  PAID: 'Đã thanh toán',
  REFUND_PENDING: 'Đang hoàn tiền',
  REFUNDED: 'Đã hoàn tiền',
};

export function formatRemaining(ms: number): string {
  if (ms <= 0) return 'Đã hết hạn thanh toán';
  if (ms < 60_000) return 'Còn dưới 1 phút để thanh toán';
  const minutes = Math.floor(ms / 60_000);
  if (ms < 3_600_000) return `Còn ${minutes} phút để thanh toán`;
  const hours = Math.floor(ms / 3_600_000);
  const remainingMinutes = Math.floor((ms % 3_600_000) / 60_000);
  return `Còn ${hours} giờ ${remainingMinutes} phút để thanh toán`;
}

export function isPayable(order: Order, now = Date.now()): boolean {
  return (
    order.status === 'AWAITING_PAYMENT' &&
    order.payment_deadline !== null &&
    now < Date.parse(order.payment_deadline)
  );
}
