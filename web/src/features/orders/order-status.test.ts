import { expect, test } from 'vitest';
import { buyerCanCancel, buyerCanComplete, ORDER_STATUS_TABS, parseOrderStatus, sellerActionsFor } from './order-status';

test.each([
  ['PENDING', []],
  ['PENDING_CONFIRMATION', ['confirm', 'cancel']],
  ['CONFIRMED', ['ship', 'cancel']],
  ['SHIPPING', ['deliver']],
  ['DELIVERED', []],
  ['COMPLETED', []],
  ['CANCELLED', []],
] as const)('seller actions for %s', (status, actions) => {
  expect(sellerActionsFor(status)).toEqual(actions);
});

test('buyer may cancel while awaiting payment and pending confirmation, and complete only when delivered', () => {
  expect(buyerCanCancel('AWAITING_PAYMENT')).toBe(true);
  expect(buyerCanCancel('PENDING_CONFIRMATION')).toBe(true);
  expect(buyerCanCancel('CONFIRMED')).toBe(false);
  expect(buyerCanComplete('DELIVERED')).toBe(true);
  expect(buyerCanComplete('SHIPPING')).toBe(false);
});

test('ORDER_STATUS_TABS starts with AWAITING_PAYMENT', () => {
  expect(ORDER_STATUS_TABS[1]).toEqual({ value: 'AWAITING_PAYMENT', label: 'Chờ thanh toán' });
});

test('parseOrderStatus rejects unknown values', () => {
  expect(parseOrderStatus('SHIPPING')).toBe('SHIPPING');
  expect(parseOrderStatus('shipping')).toBeUndefined();
  expect(parseOrderStatus('toString')).toBeUndefined();
  expect(parseOrderStatus(null)).toBeUndefined();
});
