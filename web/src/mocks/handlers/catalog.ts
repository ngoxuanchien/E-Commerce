import type { ProductInput, ProductPatch, ShopCreate, ShopUpdate, UploadRequest } from '@/api/types';
import type { MockDb, SearchQuery } from '../db';
import { validation } from '../mock-error';
import { authorization, handle, http, jsonBody, productStatusParam, searchParams } from './http';

const SORTS = ['relevance', 'newest', 'price_asc', 'price_desc'] as const;

export function catalogHandlers(db: () => MockDb) {
  const seller = (request: Request) => db().requireGrant(authorization(request), 'seller');
  return [
    http.get('/api/categories', ({ response }) => handle(response, () => response(200).json(db().categories()))),
    http.get('/api/products', ({ request, response }) =>
      handle(response, () => {
        const { text, int } = searchParams(request);
        const sort = text('sort');
        if (sort && !SORTS.includes(sort as (typeof SORTS)[number])) throw validation({ sort: 'unknown sort' });
        const query: SearchQuery = {
          q: text('q'), category: text('category'), shop: text('shop'), min_price: int('min_price'), max_price: int('max_price'),
          sort: sort as SearchQuery['sort'], limit: int('limit'), cursor: text('cursor'),
        };
        return response(200).json(db().search(query));
      }),
    ),
    http.get('/api/products/{id}', ({ params, request, response }) =>
      handle(response, () => {
        const header = authorization(request);
        // The gateway rejects an invalid token even on public routes.
        const viewer = header ? db().requireGrant(header).userId : undefined;
        return response(200).json(db().product(params.id, viewer));
      }),
    ),
    // Register /api/shops/me before /api/shops/{slug} so "me" is never treated as a slug.
    http.get('/api/shops/me', ({ request, response }) => handle(response, () => response(200).json(db().myShop(seller(request).userId)))),
    http.patch('/api/shops/me', ({ request, response }) =>
      handle(response, async () => response(200).json(db().updateMyShop(seller(request).userId, await jsonBody<ShopUpdate>(request)))),
    ),
    http.get('/api/shops/{slug}', ({ params, response }) => handle(response, () => response(200).json(db().shopBySlug(params.slug)))),
    http.post('/api/shops', ({ request, response }) =>
      handle(response, async () => {
        const grant = db().requireGrant(authorization(request));
        return response(201).json(db().createShop(grant.userId, await jsonBody<ShopCreate>(request)));
      }),
    ),
    http.get('/api/seller/products', ({ request, response }) =>
      handle(response, () => {
        const { text, int } = searchParams(request);
        return response(200).json(db().sellerProducts(seller(request).userId, productStatusParam(text('status')), int('limit'), text('cursor')));
      }),
    ),
    http.post('/api/seller/products', ({ request, response }) =>
      handle(response, async () => response(201).json(db().createProduct(seller(request).userId, await jsonBody<ProductInput>(request)))),
    ),
    http.patch('/api/seller/products/{id}', ({ params, request, response }) =>
      handle(response, async () => response(200).json(db().updateProduct(seller(request).userId, params.id, await jsonBody<ProductPatch>(request)))),
    ),
    http.delete('/api/seller/products/{id}', ({ params, request, response }) =>
      handle(response, () => {
        db().archiveProduct(seller(request).userId, params.id);
        return response(204).empty();
      }),
    ),
    http.post('/api/seller/uploads', ({ request, response }) =>
      handle(response, async () => {
        const body = await jsonBody<UploadRequest>(request);
        return response(200).json(db().createUpload(seller(request).userId, body.content_type));
      }),
    ),
  ];
}
