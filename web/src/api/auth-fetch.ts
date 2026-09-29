type AuthFetchDeps = {
  getToken: () => string | null;
  refresh: () => Promise<string | null>;
  onAuthFailure: () => void;
};

function withToken(request: Request, token: string | null): Request {
  if (!token) return request;
  const headers = new Headers(request.headers);
  headers.set('Authorization', `Bearer ${token}`);
  return new Request(request, { headers });
}

function isAuthEndpoint(request: Request): boolean {
  return new URL(request.url).pathname.startsWith('/api/auth/');
}

/** fetch for openapi-fetch: adds the bearer token; on 401 refreshes once (single-flight) and retries once. */
export function createAuthFetch(deps: AuthFetchDeps): (input: Request) => Promise<Response> {
  return async (input) => {
    const retry = input.clone();
    const response = await fetch(withToken(input, deps.getToken()));
    if (response.status !== 401 || isAuthEndpoint(input)) return response;

    const token = await deps.refresh();
    if (!token) {
      deps.onAuthFailure();
      return response;
    }
    return fetch(withToken(retry, token));
  };
}
