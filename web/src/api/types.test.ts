import { expectTypeOf, test } from 'vitest';
import type { CartItem, ErrorCode, OrderStatus, Paths, Product, ProductInput, SkuInput, TokenResponse } from './types';

test('generated types match the OpenAPI contract', () => {
  expectTypeOf<Product['min_price']>().toEqualTypeOf<number | null>();
  expectTypeOf<CartItem['image_url']>().toEqualTypeOf<string | null>();
  expectTypeOf<OrderStatus>().toEqualTypeOf<
    'PENDING' | 'AWAITING_PAYMENT' | 'PENDING_CONFIRMATION' | 'CONFIRMED' | 'SHIPPING' | 'DELIVERED' | 'COMPLETED' | 'CANCELLED'
  >();
  expectTypeOf<TokenResponse['token_type']>().toEqualTypeOf<'Bearer'>();
  expectTypeOf<ErrorCode>().toExtend<string>();
  // ProductInput = allOf [ProductPatch, required members]: the create fields are required, the rest optional.
  expectTypeOf<ProductInput['name']>().toEqualTypeOf<string>();
  expectTypeOf<ProductInput['category_id']>().toEqualTypeOf<string>();
  expectTypeOf<ProductInput['status']>().toEqualTypeOf<'draft' | 'active' | 'archived'>();
  expectTypeOf<ProductInput['skus']>().toEqualTypeOf<SkuInput[]>();
  expectTypeOf<ProductInput['description']>().toEqualTypeOf<string | undefined>();
  expectTypeOf<keyof Paths>().toExtend<string>();
  expectTypeOf<'/api/checkout'>().toExtend<keyof Paths>();
});
