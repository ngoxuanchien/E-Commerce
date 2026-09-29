import { expect, test } from 'vitest';
import { safeNext } from './safe-next';

test.each([
  ['/cart', '/cart'],
  ['/orders?status=SHIPPING', '/orders?status=SHIPPING'],
  ['//evil.example', '/'],
  ['https://evil.example', '/'],
  ['/\\evil.example', '/'],
  ['javascript:alert(1)', '/'],
  ['', '/'],
  [null, '/'],
  [undefined, '/'],
])('safeNext(%j) → %j', (input, expected) => {
  expect(safeNext(input)).toBe(expected);
});
