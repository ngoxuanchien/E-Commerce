import { expectTypeOf, test } from 'vitest';
import type { CartItem, ErrorCode, OrderStatus, Paths, Product, ProductInput, TokenResponse } from './types';

test('generated types match the OpenAPI contract', () => {
  expectTypeOf<Product['min_price']>().toEqualTypeOf<number | null>();
  expectTypeOf<CartItem['image_url']>().toEqualTypeOf<string | null>();
  expectTypeOf<OrderStatus>().toEqualTypeOf<
    'PENDING' | 'PENDING_CONFIRMATION' | 'CONFIRMED' | 'SHIPPING' | 'DELIVERED' | 'COMPLETED' | 'CANCELLED'
  >();
  expectTypeOf<TokenResponse['token_type']>().toEqualTypeOf<'Bearer'>();
  expectTypeOf<ErrorCode>().toExtend<string>();
  expectTypeOf<ProductInput['name']>().toEqualTypeOf<string>();
  expectTypeOf<keyof Paths>().toExtend<string>();
  expectTypeOf<'/api/checkout'>().toExtend<keyof Paths>();
});
