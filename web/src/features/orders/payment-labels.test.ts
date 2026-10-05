import { expect, test } from 'vitest';
import { formatRemaining, isPayable, PAYMENT_METHOD_LABEL, PAYMENT_STATUS_LABEL } from './payment-labels';
import type { Order } from '@/api/types';

function makeOrder(overrides: Partial<Order> = {}): Order {
  const now = new Date().toISOString();
  return {
    id: 'order1',
    checkout_id: 'checkout1',
    status: 'AWAITING_PAYMENT',
    buyer_id: 'buyer1',
    shop: { id: 'shop1', name: 'Shop 1', slug: 'shop-1' },
    items: [],
    subtotal: 0,
    shipping_fee: 0,
    total: 0,
    shipping_address: { full_name: 'Test', phone: '123', line1: 'Line 1', ward: 'Ward', district: 'District', province: 'Province' },
    payment_method: 'COD',
    payment_status: 'NONE',
    payment_deadline: new Date(Date.now() + 60_000).toISOString(),
    commission_rate_bps: 0,
    commission_amount: 0,
    delivered_at: null,
    cancel_reason: null,
    history: [],
    created_at: now,
    updated_at: now,
    ...overrides,
  };
}

test.each([
  [0, 'Đã hết hạn thanh toán'],
  [-1, 'Đã hết hạn thanh toán'],
  [59_000, 'Còn dưới 1 phút để thanh toán'],
  [60_000, 'Còn 1 phút để thanh toán'],
  [14 * 60_000 + 59_000, 'Còn 14 phút để thanh toán'],
  [3_600_000 + 5 * 60_000, 'Còn 1 giờ 5 phút để thanh toán'],
])('formatRemaining: %i ms → %s', (ms, expected) => {
  expect(formatRemaining(ms)).toBe(expected);
});

test('isPayable: AWAITING with future deadline → true', () => {
  const order = makeOrder({
    status: 'AWAITING_PAYMENT',
    payment_status: 'UNPAID',
    payment_deadline: new Date(Date.now() + 60_000).toISOString(),
  });
  expect(isPayable(order, Date.now())).toBe(true);
});

test('isPayable: AWAITING with past deadline → false', () => {
  const order = makeOrder({
    status: 'AWAITING_PAYMENT',
    payment_status: 'UNPAID',
    payment_deadline: new Date(Date.now() - 60_000).toISOString(),
  });
  expect(isPayable(order, Date.now())).toBe(false);
});

test('isPayable: PENDING_CONFIRMATION → false', () => {
  const order = makeOrder({
    status: 'PENDING_CONFIRMATION',
    payment_status: 'NONE',
    payment_deadline: new Date(Date.now() + 60_000).toISOString(),
  });
  expect(isPayable(order, Date.now())).toBe(false);
});

test('isPayable: null deadline → false', () => {
  const order = makeOrder({
    status: 'AWAITING_PAYMENT',
    payment_status: 'UNPAID',
    payment_deadline: null,
  });
  expect(isPayable(order, Date.now())).toBe(false);
});

test('PAYMENT_METHOD_LABEL exports correct labels', () => {
  expect(PAYMENT_METHOD_LABEL.COD).toBe('Thanh toán khi nhận hàng (COD)');
  expect(PAYMENT_METHOD_LABEL.VNPAY).toBe('VNPay');
  expect(PAYMENT_METHOD_LABEL.STRIPE).toBe('Thẻ quốc tế (Stripe)');
});

test('PAYMENT_STATUS_LABEL exports correct labels', () => {
  expect(PAYMENT_STATUS_LABEL.UNPAID).toBe('Chưa thanh toán');
  expect(PAYMENT_STATUS_LABEL.PAID).toBe('Đã thanh toán');
  expect(PAYMENT_STATUS_LABEL.REFUND_PENDING).toBe('Đang hoàn tiền');
  expect(PAYMENT_STATUS_LABEL.REFUNDED).toBe('Đã hoàn tiền');
});
