import { expect, test, beforeEach, vi } from 'vitest';
import { payKey, renewPayKey } from './pay-key';

beforeEach(() => {
  sessionStorage.clear();
});

test('payKey returns the same value twice and stores it under pay:checkoutId', () => {
  const key1 = payKey('c1');
  const key2 = payKey('c1');

  expect(key1).toBe(key2);
  expect(sessionStorage.getItem('pay:c1')).toBe(key1);
});

test('renewPayKey returns a different value and payKey returns the renewed one', () => {
  const initial = payKey('c1');
  const renewed = renewPayKey('c1');

  expect(renewed).not.toBe(initial);
  expect(payKey('c1')).toBe(renewed);
});

test('payKey still returns a UUID-shaped string when sessionStorage throws', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });

  const key = payKey('c1');
  expect(key).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);
});
