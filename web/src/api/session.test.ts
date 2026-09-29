import { afterEach, expect, test, vi } from 'vitest';
import type { User } from './types';
import { clearSession, getAccessToken, getSession, refreshSession, setSession, subscribeSession } from './session';

const user: User = { id: 'u1', email: 'an@example.com', full_name: 'An', roles: ['buyer'], created_at: '2026-09-28T00:00:00Z' };

afterEach(() => {
  vi.unstubAllGlobals();
  clearSession();
});

test('concurrent refreshes share a single request (single-flight)', async () => {
  const fetchMock = vi.fn(async () => {
    await new Promise((r) => setTimeout(r, 10));
    return Response.json({ access_token: 'at-1', token_type: 'Bearer', expires_in: 900, user });
  });
  vi.stubGlobal('fetch', fetchMock);
  const listener = vi.fn();
  const unsubscribe = subscribeSession(listener);

  const results = await Promise.all([refreshSession(), refreshSession(), refreshSession()]);

  expect(fetchMock).toHaveBeenCalledTimes(1);
  const req = (fetchMock.mock.calls[0] as unknown as [Request])[0];
  expect(new URL(req.url).pathname).toBe('/api/auth/refresh');
  expect(req.method).toBe('POST');
  expect(results.every((r) => r?.access_token === 'at-1')).toBe(true);
  expect(getAccessToken()).toBe('at-1');
  expect(listener).toHaveBeenCalled();
  unsubscribe();
});

test('a failed refresh clears the session and a later refresh makes a new request', async () => {
  const fetchMock = vi.fn(async () => new Response('{"error":{"code":"UNAUTHENTICATED","message":"x"}}', { status: 401 }));
  vi.stubGlobal('fetch', fetchMock);
  await expect(refreshSession()).resolves.toBeNull();
  expect(getSession()).toBeNull();
  await refreshSession();
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test('a 503 or network error on refresh keeps the session and rejects with a retryable error', async () => {
  setSession({ access_token: 'at-keep', token_type: 'Bearer', expires_in: 900, user });
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":{"code":"UPSTREAM_UNAVAILABLE","message":"down"}}', { status: 503 })));
  await expect(refreshSession()).rejects.toMatchObject({ code: 'UPSTREAM_UNAVAILABLE', retryable: true });
  vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
  await expect(refreshSession()).rejects.toMatchObject({ code: 'UPSTREAM_UNAVAILABLE' });
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":{"code":"RATE_LIMITED","message":"slow"}}', { status: 429 })));
  await expect(refreshSession()).rejects.toMatchObject({ code: 'RATE_LIMITED' });
  expect(getAccessToken()).toBe('at-keep');
});
