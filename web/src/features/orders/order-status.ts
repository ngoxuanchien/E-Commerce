import type { ActorRole, OrderStatus } from '@/api/types';

export const ORDER_STATUS_LABEL: Record<OrderStatus, string> = {
  PENDING: 'Đang xử lý',
  AWAITING_PAYMENT: 'Chờ thanh toán',
  PENDING_CONFIRMATION: 'Chờ xác nhận',
  CONFIRMED: 'Đã xác nhận',
  SHIPPING: 'Đang giao',
  DELIVERED: 'Đã giao',
  COMPLETED: 'Hoàn thành',
  CANCELLED: 'Đã huỷ',
};

export const ORDER_STATUS_TONE: Record<OrderStatus, string> = {
  PENDING: 'bg-muted text-muted-foreground',
  AWAITING_PAYMENT: 'bg-turmeric/10 text-turmeric',
  PENDING_CONFIRMATION: 'bg-turmeric/10 text-turmeric',
  CONFIRMED: 'bg-tarp/10 text-tarp',
  SHIPPING: 'bg-tarp/10 text-tarp',
  DELIVERED: 'bg-jade/10 text-jade',
  COMPLETED: 'bg-jade text-white',
  CANCELLED: 'bg-muted text-muted-foreground',
};

const TAB_STATUSES = ['AWAITING_PAYMENT', 'PENDING_CONFIRMATION', 'CONFIRMED', 'SHIPPING', 'DELIVERED', 'COMPLETED', 'CANCELLED'] as const;

export const ORDER_STATUS_TABS: ReadonlyArray<{ value: OrderStatus | undefined; label: string }> = [
  { value: undefined, label: 'Tất cả' },
  ...TAB_STATUSES.map((value) => ({ value, label: ORDER_STATUS_LABEL[value] })),
];

export function parseOrderStatus(value: string | null): OrderStatus | undefined {
  return value !== null && Object.hasOwn(ORDER_STATUS_LABEL, value) ? (value as OrderStatus) : undefined;
}

export const ACTOR_LABEL: Record<ActorRole, string> = { buyer: 'Người mua', seller: 'Người bán', system: 'Hệ thống' };

export type SellerAction = 'confirm' | 'ship' | 'deliver' | 'cancel';

/** Seller moves allowed by the WS3 state machine. */
export function sellerActionsFor(status: OrderStatus): SellerAction[] {
  switch (status) {
    case 'PENDING_CONFIRMATION':
      return ['confirm', 'cancel'];
    case 'CONFIRMED':
      return ['ship', 'cancel'];
    case 'SHIPPING':
      return ['deliver'];
    default:
      return [];
  }
}

export const buyerCanCancel = (status: OrderStatus) => status === 'PENDING_CONFIRMATION' || status === 'AWAITING_PAYMENT';
export const buyerCanComplete = (status: OrderStatus) => status === 'DELIVERED';
export const shortId = (id: string) => `#${id.replace(/-/g, '').slice(-8).toUpperCase()}`;
