import { describe, expect, test, vi } from 'vitest';
import { ApiError, ERROR_MESSAGES, applyFieldErrors, errorMessage, fieldErrorsOf, isRetryable, shortagesOf, unwrap } from './errors';

const body = (code: string, details?: object) => ({ error: { code, message: 'server text', ...(details ? { details } : {}) } });

describe('ApiError.from', () => {
  test('uses the error code from the body', () => {
    const err = ApiError.from(409, body('OUT_OF_STOCK'));
    expect(err.code).toBe('OUT_OF_STOCK');
    expect(err.status).toBe(409);
  });

  test.each([
    [401, 'UNAUTHENTICATED'],
    [413, 'PAYLOAD_TOO_LARGE'],
    [429, 'RATE_LIMITED'],
    [503, 'UPSTREAM_UNAVAILABLE'],
    [500, 'INTERNAL'],
    [418, 'INTERNAL'],
  ])('falls back from status %d to %s when the body is not an error envelope', (status, code) => {
    expect(ApiError.from(status, '<html>').code).toBe(code);
  });

  test('RATE_LIMITED and UPSTREAM_UNAVAILABLE are retryable, others are not', () => {
    expect(ApiError.from(429, body('RATE_LIMITED')).retryable).toBe(true);
    expect(isRetryable(ApiError.from(503, body('UPSTREAM_UNAVAILABLE')))).toBe(true);
    expect(isRetryable(ApiError.from(409, body('CONFLICT')))).toBe(false);
    expect(isRetryable(new Error('x'))).toBe(false);
  });
});

describe('unwrap', () => {
  test('returns data on success and throws ApiError otherwise', async () => {
    await expect(unwrap(Promise.resolve({ data: { ok: 1 }, response: new Response(null, { status: 200 }) }))).resolves.toEqual({ ok: 1 });
    await expect(
      unwrap(Promise.resolve({ error: body('NOT_FOUND'), response: new Response(null, { status: 404 }) })),
    ).rejects.toMatchObject({ code: 'NOT_FOUND', status: 404 });
  });

  test('maps network failures to UPSTREAM_UNAVAILABLE', async () => {
    await expect(unwrap(Promise.reject(new TypeError('Failed to fetch')))).rejects.toMatchObject({ code: 'UPSTREAM_UNAVAILABLE', status: 0 });
  });
});

describe('messages', () => {
  test('every error code has a Vietnamese message', () => {
    expect(Object.keys(ERROR_MESSAGES)).toHaveLength(12);
    expect(errorMessage(ApiError.from(409, body('OUT_OF_STOCK')))).toBe(ERROR_MESSAGES.OUT_OF_STOCK);
  });

  test('context overrides win and unknown errors get the generic message', () => {
    expect(errorMessage(ApiError.from(401, body('UNAUTHENTICATED')), { UNAUTHENTICATED: 'Sai mật khẩu' })).toBe('Sai mật khẩu');
    expect(errorMessage(new Error('boom'))).toBe(ERROR_MESSAGES.INTERNAL);
  });
});

describe('details helpers', () => {
  test('shortagesOf reads OUT_OF_STOCK details', () => {
    const err = ApiError.from(409, body('OUT_OF_STOCK', { shortages: [{ sku_id: 's1', requested: 2, available: 1 }] }));
    expect(shortagesOf(err)).toEqual([{ sku_id: 's1', requested: 2, available: 1 }]);
    expect(shortagesOf(ApiError.from(409, body('CONFLICT')))).toEqual([]);
  });

  test('fieldErrorsOf converts JSON paths to form paths and renames fields', () => {
    const err = ApiError.from(400, body('VALIDATION_ERROR', { fields: { 'skus[0].price': 'min 1000', image_keys: 'too many', 'image_keys[2]': 'foreign' } }));
    expect(fieldErrorsOf(err, { image_keys: 'images' })).toEqual({ 'skus.0.price': 'min 1000', images: 'too many', 'images.2': 'foreign' });
  });

  test('applyFieldErrors sets a Vietnamese message per field and reports whether it applied', () => {
    const setError = vi.fn();
    const err = ApiError.from(400, body('VALIDATION_ERROR', { fields: { 'shipping_address.phone': 'required' } }));
    expect(applyFieldErrors(err, setError, { stripPrefix: 'shipping_address.' })).toBe(true);
    expect(setError).toHaveBeenCalledWith('phone', { type: 'server', message: 'Giá trị không hợp lệ' });
    expect(applyFieldErrors(ApiError.from(409, body('CONFLICT')), setError)).toBe(false);
  });
});
