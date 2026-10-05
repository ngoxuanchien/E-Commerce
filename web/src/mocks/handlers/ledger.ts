import type { PayoutRequest } from '@/api/types';
import type { MockDb } from '../db';
import { authorization, handle, http, jsonBody, searchParams } from './http';

export function ledgerHandlers(db: () => MockDb) {
  const seller = (request: Request) => db().requireGrant(authorization(request), 'seller').userId;

  return [
    http.get('/api/seller/wallet', ({ request, response }) =>
      handle(response, () => response(200).json(db().wallet(seller(request)))),
    ),
    http.get('/api/seller/wallet/entries', ({ request, response }) =>
      handle(response, () => {
        const { int, text } = searchParams(request);
        return response(200).json(db().walletEntries(seller(request), int('limit'), text('cursor')));
      }),
    ),
    http.post('/api/seller/payouts', ({ request, response }) =>
      handle(response, async () => {
        const userId = seller(request);
        const key = request.headers.get('Idempotency-Key') ?? '';
        return response(201).json(db().createPayout(userId, key, await jsonBody<PayoutRequest>(request)));
      }),
    ),
    http.get('/api/seller/payouts', ({ request, response }) =>
      handle(response, () => {
        const { text, int } = searchParams(request);
        return response(200).json(db().payouts(seller(request), text('status'), int('limit'), text('cursor')));
      }),
    ),
  ];
}
