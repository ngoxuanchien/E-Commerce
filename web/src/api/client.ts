import createClient from 'openapi-fetch';
import { createAuthFetch } from './auth-fetch';
import { apiBaseUrl } from './base-url';
import { getAccessToken, notifyAuthFailure, refreshSession } from './session';
import type { Paths } from './types';

export const api = createClient<Paths>({
  baseUrl: apiBaseUrl(),
  credentials: 'include',
  fetch: createAuthFetch({
    getToken: getAccessToken,
    refresh: async () => (await refreshSession())?.access_token ?? null,
    onAuthFailure: notifyAuthFailure,
  }),
});
