import { HttpResponse } from 'msw';
import { createOpenApiHttp } from 'openapi-msw';
import type { OrderStatus, Paths, ProductStatus } from '@/api/types';
import { MockError, validation } from '../mock-error';

/** MSW `http` typed by the OpenAPI paths: wrong paths, params or response bodies fail to compile. */
// baseUrl '*' matches any origin, so the same handlers work in the browser, jsdom and plain Node.
export const http = createOpenApiHttp<Paths>({ baseUrl: '*' });

type UntypedResponder<R> = { untyped: (response: Response) => R };

/** Runs a resolver and converts MockError into the standard error envelope. */
export async function handle<R>(response: UntypedResponder<R>, run: () => R | Promise<R>): Promise<R> {
  try {
    return await run();
  } catch (err) {
    if (!(err instanceof MockError)) throw err;
    const error = { code: err.code, message: err.message, ...(err.details ? { details: err.details } : {}) };
    return response.untyped(HttpResponse.json({ error }, { status: err.status }));
  }
}

export const authorization = (request: Request) => request.headers.get('authorization');

export function searchParams(request: Request) {
  const params = new URL(request.url).searchParams;
  const text = (name: string) => params.get(name) || undefined;
  const int = (name: string) => {
    const value = params.get(name);
    if (value === null || value === '') return undefined;
    if (!/^\d+$/.test(value)) throw validation({ [name]: 'must be an integer' });
    return Number(value);
  };
  return { text, int };
}

const ORDER_STATUSES: OrderStatus[] = ['PENDING', 'PENDING_CONFIRMATION', 'AWAITING_PAYMENT', 'CONFIRMED', 'SHIPPING', 'DELIVERED', 'COMPLETED', 'CANCELLED'];
const PRODUCT_STATUSES: ProductStatus[] = ['draft', 'active', 'archived'];

export function orderStatusParam(value: string | undefined): OrderStatus | undefined {
  if (value === undefined) return undefined;
  if (!ORDER_STATUSES.includes(value as OrderStatus)) throw validation({ status: 'unknown status' });
  return value as OrderStatus;
}

export function productStatusParam(value: string | undefined): ProductStatus | undefined {
  if (value === undefined) return undefined;
  if (!PRODUCT_STATUSES.includes(value as ProductStatus)) throw validation({ status: 'unknown status' });
  return value as ProductStatus;
}

/** JSON body, or `{}` when the (optional) body is empty. */
export async function jsonBody<T>(request: Request): Promise<T> {
  const text = await request.text();
  if (!text) return {} as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw validation({ body: 'invalid JSON' });
  }
}
