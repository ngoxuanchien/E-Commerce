// Minimal stand-in for the gateway + identity-svc session contract, used to prove the SPA
// restores its session from an HttpOnly refresh cookie (Path=/api/auth) after a reload.
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';

const PORT = Number(process.env.STUB_GATEWAY_PORT ?? 18080);
const USER = { id: '0199a000-0000-7000-8000-000000000001', email: 'an@example.com', full_name: 'An Nguyễn', roles: ['buyer'], created_at: '2026-09-20T01:00:00Z' };
const refreshTokens = new Set();
const accessTokens = new Set();

const cookie = (value, maxAge) => `refresh_token=${value}; HttpOnly; Secure; SameSite=Lax; Path=/api/auth; Max-Age=${maxAge}`;
const error = (code, message) => ({ error: { code, message } });

function send(res, status, body, headers = {}) {
  res.writeHead(status, { 'Content-Type': 'application/json', ...headers });
  res.end(body === undefined ? undefined : JSON.stringify(body));
}

function issue(res) {
  const refresh = randomUUID();
  const access = `stub-at-${randomUUID()}`;
  refreshTokens.add(refresh);
  accessTokens.add(access);
  send(res, 200, { access_token: access, token_type: 'Bearer', expires_in: 900, user: USER }, { 'Set-Cookie': cookie(refresh, 2592000) });
}

const readCookie = (req) => /(?:^|;\s*)refresh_token=([^;]+)/.exec(req.headers.cookie ?? '')?.[1];

async function readJson(req) {
  let data = '';
  for await (const chunk of req) data += chunk;
  return data ? JSON.parse(data) : {};
}

createServer(async (req, res) => {
  const { pathname } = new URL(req.url, 'http://localhost');
  if (pathname === '/healthz') return send(res, 200, { status: 'ok' });
  if (req.method === 'POST' && pathname === '/api/auth/login') {
    const body = await readJson(req);
    return body.email === USER.email && body.password === 'matkhau123' ? issue(res) : send(res, 401, error('UNAUTHENTICATED', 'invalid credentials'));
  }
  if (req.method === 'POST' && pathname === '/api/auth/refresh') {
    const token = readCookie(req);
    // Rotation: each refresh token works once, like identity-svc.
    if (token && refreshTokens.delete(token)) return issue(res);
    return send(res, 401, error('UNAUTHENTICATED', 'invalid refresh token'), { 'Set-Cookie': cookie('', 0) });
  }
  if (req.method === 'POST' && pathname === '/api/auth/logout') {
    const token = readCookie(req);
    if (token) refreshTokens.delete(token);
    res.writeHead(204, { 'Set-Cookie': cookie('', 0) });
    return res.end();
  }
  const bearer = /^Bearer (.+)$/.exec(req.headers.authorization ?? '')?.[1];
  if (bearer && !accessTokens.has(bearer)) return send(res, 401, error('UNAUTHENTICATED', 'invalid token'));
  if (pathname === '/api/categories') return send(res, 200, []);
  if (pathname === '/api/products') return send(res, 200, { items: [], next_cursor: null });
  if (pathname === '/api/users/me' || pathname === '/api/cart') {
    if (!bearer) return send(res, 401, error('UNAUTHENTICATED', 'missing token'));
    return send(res, 200, pathname === '/api/cart' ? { shops: [], subtotal: 0 } : USER);
  }
  return send(res, 404, error('NOT_FOUND', 'route not implemented by the stub gateway'));
}).listen(PORT, '127.0.0.1', () => console.log(`stub gateway listening on http://127.0.0.1:${PORT}`));
