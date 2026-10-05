import { describe, expect, test, vi } from 'vitest';
import { api } from '@/api/client';
import { ApiError, unwrap } from '@/api/errors';
import { getAccessToken, setAuthFailureHandler, setSession } from '@/api/session';
import { server } from '@/test/server';
import { SEED } from './seed';

async function login(email: string) {
  const token = await unwrap(api.POST('/api/auth/login', { body: { email, password: SEED.password } }));
  setSession(token);
  return token;
}

function countRefreshes() {
  const counter = { n: 0 };
  server.events.on('request:start', ({ request }) => {
    if (new URL(request.url).pathname === '/api/auth/refresh') counter.n += 1;
  });
  return counter;
}

describe('mock API through the typed client', () => {
  test('login then GET /api/users/me', async () => {
    await login('an@example.com');
    const me = await unwrap(api.GET('/api/users/me'));
    expect(me.full_name).toBe('An Nguyễn');
  });

  test('anonymous user route → refresh fails → auth failure reported once, 401 surfaced', async () => {
    const onFailure = vi.fn();
    setAuthFailureHandler(onFailure);
    await expect(unwrap(api.GET('/api/cart'))).rejects.toMatchObject({ status: 401, code: 'UNAUTHENTICATED' });
    expect(onFailure).toHaveBeenCalledTimes(1);
  });

  test('an expired access token is refreshed transparently while the session cookie is valid', async () => {
    const token = await login('an@example.com');
    setSession({ ...token, access_token: 'mock-at.expired' });
    await expect(unwrap(api.GET('/api/cart'))).resolves.toEqual({ shops: [], subtotal: 0 });
    expect(getAccessToken()).not.toBe('mock-at.expired');
  });

  test('three parallel 401s trigger exactly one refresh', async () => {
    const token = await login('an@example.com');
    setSession({ ...token, access_token: 'mock-at.expired' });
    const refreshes = countRefreshes();
    await Promise.all([unwrap(api.GET('/api/cart')), unwrap(api.GET('/api/orders')), unwrap(api.GET('/api/users/me'))]);
    expect(refreshes.n).toBe(1);
    server.events.removeAllListeners();
  });

  test('wrong password is a 401 without any refresh attempt', async () => {
    const refreshes = countRefreshes();
    const err = await unwrap(api.POST('/api/auth/login', { body: { email: 'an@example.com', password: 'sai-mat-khau' } })).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 401 });
    expect(refreshes.n).toBe(0);
    server.events.removeAllListeners();
  });

  test('checkout OUT_OF_STOCK carries shortages in the error envelope', async () => {
    await login('an@example.com');
    await unwrap(api.PUT('/api/cart/items/{sku_id}', { params: { path: { sku_id: SEED.skus.vase } }, body: { quantity: 3 } }));
    const err = await unwrap(
      api.POST('/api/checkout', {
        params: { header: { 'Idempotency-Key': 'k-1' } },
        body: { shipping_address: { full_name: 'A', phone: '0901234567', line1: '1', ward: 'W', district: 'D', province: 'P' }, payment_method: 'COD' },
      }),
    ).catch((e) => e);
    expect(err).toMatchObject({ status: 409, code: 'OUT_OF_STOCK', details: { shortages: [{ sku_id: SEED.skus.vase, requested: 3, available: 1 }] } });
  });

  test('seller routes answer 403 for buyers', async () => {
    await login('an@example.com');
    await expect(unwrap(api.GET('/api/shops/me'))).rejects.toMatchObject({ status: 403, code: 'FORBIDDEN' });
  });

  test('payment round trip: POST /api/payments, /api/__mock/payments/{id}/succeed, GET /api/payments/{id}', async () => {
    await login('an@example.com');

    // Use the known SKU from the seed: teeM (id(58))
    const teeSkuId = '0199a000-0000-7000-8000-000000000058';

    // Create cart and checkout
    await unwrap(api.PUT('/api/cart/items/{sku_id}', { params: { path: { sku_id: teeSkuId } }, body: { quantity: 1 } }));
    const checkout = await unwrap(
      api.POST('/api/checkout', {
        params: { header: { 'Idempotency-Key': 'payment-test-key' } },
        body: {
          shipping_address: { full_name: 'An Nguyễn', phone: '0901234567', line1: '12 Lý Tự Trọng', ward: 'Bến Nghé', district: 'Quận 1', province: 'TP. Hồ Chí Minh' },
          payment_method: 'VNPAY',
        },
      }),
    );

    // Create payment
    const payment = await unwrap(
      api.POST('/api/payments', {
        params: { header: { 'Idempotency-Key': `pay:${checkout.checkout_id}` } },
        body: { checkout_id: checkout.checkout_id },
      }),
    );
    expect(payment.status).toBe('PENDING');
    expect(payment.method).toBe('VNPAY');

    // Submit mock payment
    const mockResponse = await fetch(`/api/__mock/payments/${payment.id}/succeed`, { method: 'POST' });
    expect(mockResponse.status).toBe(204);

    // Get payment result
    const result = await unwrap(api.GET('/api/payments/{id}', { params: { path: { id: payment.id } } }));
    expect(result.status).toBe('SUCCEEDED');
  });
});
