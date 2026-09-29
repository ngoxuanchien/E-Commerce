import { expect, test } from 'vitest';
import { parseProductQuery } from './search-params';

test('parses valid params and drops invalid ones', () => {
  expect(parseProductQuery(new URLSearchParams('q=%20ao%20thun%20&category=ao-thun&min_price=1000&max_price=abc&sort=cheap'))).toEqual({
    q: 'ao thun',
    category: 'ao-thun',
    min_price: 1000,
    max_price: undefined,
    sort: undefined,
  });
  expect(parseProductQuery(new URLSearchParams('sort=price_desc')).sort).toBe('price_desc');
});
