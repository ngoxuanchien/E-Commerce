import type { LoginRequest, RegisterRequest } from '@/api/types';
import type { MockDb } from '../db';
import { authorization, handle, http, jsonBody } from './http';

export function identityHandlers(db: () => MockDb) {
  return [
    http.post('/api/auth/register', ({ request, response }) =>
      handle(response, async () => response(201).json(db().register(await jsonBody<RegisterRequest>(request)))),
    ),
    http.post('/api/auth/login', ({ request, response }) =>
      handle(response, async () => response(200).json(db().login(await jsonBody<LoginRequest>(request)))),
    ),
    http.post('/api/auth/refresh', ({ response }) => handle(response, () => response(200).json(db().refresh()))),
    http.post('/api/auth/logout', ({ response }) => {
      db().logout();
      return response(204).empty();
    }),
    http.get('/api/users/me', ({ request, response }) =>
      handle(response, () => response(200).json(db().me(db().requireGrant(authorization(request)).userId))),
    ),
  ];
}
