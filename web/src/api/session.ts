import { apiBaseUrl } from './base-url';
import type { TokenResponse, User } from './types';

export type Session = { accessToken: string; user: User };

// The access token lives only in this module's memory. The refresh token is an HttpOnly cookie.
let session: Session | null = null;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function getSession(): Session | null {
  return session;
}

export function getAccessToken(): string | null {
  return session?.accessToken ?? null;
}

export function setSession(token: TokenResponse): void {
  session = { accessToken: token.access_token, user: token.user };
  emit();
}

export function clearSession(): void {
  if (session === null) return;
  session = null;
  emit();
}

export function subscribeSession(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

async function requestRefresh(): Promise<TokenResponse | null> {
  try {
    const res = await fetch(new Request(`${apiBaseUrl()}/api/auth/refresh`, { method: 'POST', credentials: 'include' }));
    return res.ok ? ((await res.json()) as TokenResponse) : null;
  } catch {
    return null;
  }
}

let inflight: Promise<TokenResponse | null> | null = null;

/** Single-flight: concurrent callers share one POST /api/auth/refresh (the server rotates the cookie). */
export function refreshSession(): Promise<TokenResponse | null> {
  inflight ??= requestRefresh()
    .then((token) => {
      if (token) setSession(token);
      else clearSession();
      return token;
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

let authFailureHandler: () => void = () => {
  const next = encodeURIComponent(window.location.pathname + window.location.search);
  window.location.assign(`/login?next=${next}`);
};

export function setAuthFailureHandler(handler: () => void): void {
  authFailureHandler = handler;
}

export function notifyAuthFailure(): void {
  clearSession();
  authFailureHandler();
}
