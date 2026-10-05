import { expect, test } from 'vitest';
import { payoutSchema } from './payout-schema';

test('payout-schema: empty string → error message', () => {
  const result = payoutSchema(100_000).safeParse({ amount: '' });
  expect(result.success).toBe(false);
  if (!result.success) {
    expect(result.error.issues[0].message).toBe('Vui lòng nhập số tiền');
  }
});

test('payout-schema: below min amount → error message', () => {
  const result = payoutSchema(100_000).safeParse({ amount: '49999' });
  expect(result.success).toBe(false);
  if (!result.success) {
    expect(result.error.issues[0].message).toBe('Số tiền rút tối thiểu là 50.000 ₫');
  }
});

test('payout-schema: not an integer → error message', () => {
  const result = payoutSchema(100_000).safeParse({ amount: '50000.5' });
  expect(result.success).toBe(false);
  if (!result.success) {
    expect(result.error.issues[0].message).toBe('Số tiền phải là số nguyên');
  }
});

test('payout-schema: exceeds available → error message', () => {
  const result = payoutSchema(100_000).safeParse({ amount: '500000' });
  expect(result.success).toBe(false);
  if (!result.success) {
    expect(result.error.issues[0].message).toBe('Số tiền vượt quá số dư khả dụng');
  }
});

test('payout-schema: valid amount → success', () => {
  const result = payoutSchema(457_500).safeParse({ amount: '100000' });
  expect(result.success).toBe(true);
  if (result.success) {
    expect(result.data.amount).toBe(100_000);
  }
});
