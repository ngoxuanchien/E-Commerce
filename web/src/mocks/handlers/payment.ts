import { HttpResponse, http as rawHttp } from 'msw';
import type { CreatePaymentRequest } from '@/api/types';
import type { MockDb } from '../db';
import { MockError, validation } from '../mock-error';
import { authorization, handle, http, jsonBody } from './http';

export function paymentHandlers(db: () => MockDb) {
  const user = (request: Request) => db().requireGrant(authorization(request)).userId;

  return [
    http.post('/api/payments', ({ request, response }) =>
      handle(response, async () => {
        const userId = user(request);
        const key = request.headers.get('Idempotency-Key') ?? '';
        return response(201).json(db().createPayment(userId, key, await jsonBody<CreatePaymentRequest>(request)));
      }),
    ),
    http.get('/api/payments/{id}', ({ params, request, response }) => handle(response, () => response(200).json(db().payment(user(request), params.id)))),
    // Raw mock-submit route (not in the contract, so we use the untyped msw http)
    rawHttp.post('/api/__mock/payments/:id/:outcome', ({ params }) => {
      try {
        const id = params.id as string;
        const outcome = params.outcome as string;
        if (outcome !== 'succeed' && outcome !== 'fail') {
          throw validation({ outcome: 'must be succeed or fail' });
        }
        db().submitMockPayment(id, outcome);
        return new HttpResponse(null, { status: 204 });
      } catch (err) {
        if (err instanceof MockError) {
          const error = { code: err.code, message: err.message, ...(err.details ? { details: err.details } : {}) };
          return HttpResponse.json({ error }, { status: err.status });
        }
        throw err;
      }
    }),
  ];
}
