import { afterEach, describe, expect, test, vi } from 'vitest';
import { createAuthFetch } from './auth-fetch';

const origin = 'http://localhost:3000';
const ok = () => new Response('{}', { status: 200 });
const unauthorized = () => new Response('{"error":{"code":"UNAUTHENTICATED","message":"x"}}', { status: 401 });

afterEach(() => vi.unstubAllGlobals());

function setup(responses: Array<() => Response>, refreshed: string | null = 'fresh') {
  const seen: Request[] = [];
  vi.stubGlobal('fetch', vi.fn(async (req: Request) => {
    seen.push(req);
    return (responses.shift() ?? ok)();
  }));
  const deps = { getToken: () => 'stale', refresh: vi.fn(async () => refreshed), onAuthFailure: vi.fn() };
  return { authFetch: createAuthFetch(deps), deps, seen };
}

describe('createAuthFetch', () => {
  test('attaches the bearer token', async () => {
    const { authFetch, seen } = setup([ok]);
    await authFetch(new Request(`${origin}/api/cart`));
    expect(seen[0].headers.get('Authorization')).toBe('Bearer stale');
  });

  test('on 401 refreshes once and retries with the new token and the original body', async () => {
    const { authFetch, deps, seen } = setup([unauthorized, ok]);
    const res = await authFetch(new Request(`${origin}/api/checkout`, { method: 'POST', body: '{"a":1}' }));
    expect(res.status).toBe(200);
    expect(deps.refresh).toHaveBeenCalledTimes(1);
    expect(seen).toHaveLength(2);
    expect(seen[1].headers.get('Authorization')).toBe('Bearer fresh');
    await expect(seen[1].text()).resolves.toBe('{"a":1}');
  });

  test('never refreshes for /api/auth/* (wrong password must not loop)', async () => {
    const { authFetch, deps } = setup([unauthorized]);
    const res = await authFetch(new Request(`${origin}/api/auth/login`, { method: 'POST', body: '{}' }));
    expect(res.status).toBe(401);
    expect(deps.refresh).not.toHaveBeenCalled();
  });

  test('refresh failure reports auth failure and returns the original 401', async () => {
    const { authFetch, deps } = setup([unauthorized], null);
    const res = await authFetch(new Request(`${origin}/api/cart`));
    expect(res.status).toBe(401);
    expect(deps.onAuthFailure).toHaveBeenCalledTimes(1);
  });

  test('retries only once even if the retry is also 401', async () => {
    const { authFetch, deps, seen } = setup([unauthorized, unauthorized]);
    const res = await authFetch(new Request(`${origin}/api/cart`));
    expect(res.status).toBe(401);
    expect(seen).toHaveLength(2);
    expect(deps.refresh).toHaveBeenCalledTimes(1);
  });

  test('a 401 for a token that was already refreshed retries with the current token instead of refreshing again', async () => {
    const seen: Request[] = [];
    vi.stubGlobal('fetch', vi.fn(async (req: Request) => {
      seen.push(req);
      return seen.length === 1 ? unauthorized() : ok();
    }));
    let token = 'stale';
    const refresh = vi.fn(async () => 'never-used');
    const authFetch = createAuthFetch({ getToken: () => token, refresh, onAuthFailure: vi.fn() });
    const pending = authFetch(new Request(`${origin}/api/orders`));
    token = 'fresh'; // another request refreshed while this one was in flight
    const res = await pending;
    expect(res.status).toBe(200);
    expect(refresh).not.toHaveBeenCalled();
    expect(seen[1].headers.get('Authorization')).toBe('Bearer fresh');
  });

  test('when the refresh endpoint is unreachable the request fails as retryable and the user stays logged in', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => unauthorized()));
    const onAuthFailure = vi.fn();
    const unreachable = Object.assign(new Error('down'), { code: 'UPSTREAM_UNAVAILABLE' });
    const authFetch = createAuthFetch({ getToken: () => 'stale', refresh: async () => { throw unreachable; }, onAuthFailure });
    await expect(authFetch(new Request(`${origin}/api/cart`))).rejects.toBe(unreachable);
    expect(onAuthFailure).not.toHaveBeenCalled();
  });
});
