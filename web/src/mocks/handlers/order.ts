import type { CancelRequest, CartItemUpdate, CheckoutRequest, OrderActionRequest } from '@/api/types';
import type { MockDb, SellerAction } from '../db';
import { authorization, handle, http, jsonBody, orderStatusParam, searchParams } from './http';

export function orderHandlers(db: () => MockDb) {
  const user = (request: Request) => db().requireGrant(authorization(request)).userId;
  const seller = (request: Request) => db().requireGrant(authorization(request), 'seller').userId;
  const sellerAction = (action: SellerAction) =>
    http.post(`/api/seller/orders/{id}/${action}`, async ({ params, request, response }) =>
      handle(response, async () => {
        const body = await jsonBody<OrderActionRequest>(request);
        return response(200).json(db().sellerAction(seller(request), params.id, action, body.reason));
      }),
    );
  return [
    http.get('/api/cart', ({ request, response }) => handle(response, () => response(200).json(db().cart(user(request))))),
    http.put('/api/cart/items/{sku_id}', ({ params, request, response }) =>
      handle(response, async () => {
        const { quantity } = await jsonBody<CartItemUpdate>(request);
        return response(200).json(db().setCartItem(user(request), params.sku_id, quantity));
      }),
    ),
    http.delete('/api/cart/items/{sku_id}', ({ params, request, response }) =>
      handle(response, () => response(200).json(db().removeCartItem(user(request), params.sku_id))),
    ),
    http.post('/api/checkout', ({ request, response }) =>
      handle(response, async () => {
        const userId = user(request);
        const key = request.headers.get('Idempotency-Key') ?? '';
        return response(201).json(db().checkout(userId, key, await jsonBody<CheckoutRequest>(request)));
      }),
    ),
    http.get('/api/orders', ({ request, response }) =>
      handle(response, () => {
        const { text, int } = searchParams(request);
        return response(200).json(db().buyerOrders(user(request), orderStatusParam(text('status')), int('limit'), text('cursor')));
      }),
    ),
    http.get('/api/orders/{id}', ({ params, request, response }) => handle(response, () => response(200).json(db().order(user(request), params.id)))),
    http.post('/api/orders/{id}/cancel', ({ params, request, response }) =>
      handle(response, async () => {
        const { reason } = await jsonBody<CancelRequest>(request);
        return response(200).json(db().buyerAction(user(request), params.id, 'cancel', reason));
      }),
    ),
    http.post('/api/orders/{id}/complete', ({ params, request, response }) =>
      handle(response, () => response(200).json(db().buyerAction(user(request), params.id, 'complete'))),
    ),
    http.get('/api/seller/orders', ({ request, response }) =>
      handle(response, () => {
        const { text, int } = searchParams(request);
        return response(200).json(db().sellerOrders(seller(request), orderStatusParam(text('status')), int('limit'), text('cursor')));
      }),
    ),
    sellerAction('confirm'),
    sellerAction('ship'),
    sellerAction('deliver'),
    sellerAction('cancel'),
  ];
}
