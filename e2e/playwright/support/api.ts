import { type APIRequestContext, type APIResponse, request } from '@playwright/test';

export const GATEWAY_URL = process.env.GATEWAY_URL ?? 'http://localhost:8080';
export const WEB_URL = process.env.WEB_URL ?? 'http://localhost:3000';
export const PASSWORD = 'matkhau123';

export interface Account {
  email: string;
  fullName: string;
}
export interface Sku {
  id: string;
  name: string;
  price: number;
  stock: number;
  active: boolean;
}
export interface Product {
  id: string;
  name: string;
  shop: { id: string; name: string; slug: string };
  skus: Sku[];
}
export interface Order {
  id: string;
  checkout_id: string;
  status: string;
  payment_status?: string;
  shop: { id: string; name: string };
}
interface Category {
  id: string;
  slug: string;
  children: Category[];
}

/** 8 lowercase letters: one search token, valid in shop slugs and emails. */
export function runId(): string {
  return Array.from({ length: 8 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join('');
}

/**
 * The gateway allows 10 login/register calls per minute per client IP and
 * rejects with 429 before proxying, so waiting Retry-After and resending is
 * always safe.
 */
export async function withRateLimitRetry(send: () => Promise<APIResponse>): Promise<APIResponse> {
  const deadline = Date.now() + 180_000;
  for (;;) {
    const res = await send();
    if (res.status() !== 429) return res;
    const waitMs = Number(res.headers()['retry-after'] ?? '1') * 1000;
    if (Date.now() + waitMs > deadline) return res;
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  }
}

/** A user driven through the gateway API, for test setup and assertions. */
export class ApiUser {
  private token = '';

  private constructor(
    private readonly ctx: APIRequestContext,
    readonly account: Account,
  ) {}

  static async register(fullName: string, email: string): Promise<ApiUser> {
    const user = new ApiUser(await request.newContext({ baseURL: GATEWAY_URL }), { email, fullName });
    await user.call('POST', '/api/auth/register', { email, password: PASSWORD, full_name: fullName });
    await user.login();
    return user;
  }

  async login(): Promise<void> {
    const res = await this.call<{ access_token: string }>('POST', '/api/auth/login', { email: this.account.email, password: PASSWORD });
    this.token = res.access_token;
  }

  async call<T = unknown>(method: string, path: string, data?: unknown, headers: Record<string, string> = {}): Promise<T> {
    const auth: Record<string, string> = this.token ? { Authorization: `Bearer ${this.token}` } : {};
    const res = await withRateLimitRetry(() => this.ctx.fetch(path, { method, data, headers: { ...auth, ...headers } }));
    if (!res.ok()) throw new Error(`${method} ${path} -> ${res.status()}: ${await res.text()}`);
    return (res.status() === 204 ? undefined : await res.json()) as T;
  }

  /** Opens a shop and refreshes the token so it carries the seller role. */
  async openShop(name: string, slug: string): Promise<void> {
    await this.call('POST', '/api/shops', { name, slug, description: 'Shop tạo bởi e2e.' });
    const res = await this.call<{ access_token: string }>('POST', '/api/auth/refresh');
    this.token = res.access_token;
  }

  async createProduct(name: string, stock: number, price = 120_000): Promise<Product> {
    const cats = await this.call<Category[]>('GET', '/api/categories');
    const cat = cats.flatMap((c) => c.children).find((c) => c.slug === 'do-dung-nha-bep');
    if (!cat) throw new Error('category do-dung-nha-bep not found');
    return this.call<Product>('POST', '/api/seller/products', {
      category_id: cat.id,
      name,
      description: `${name} — sản phẩm kiểm thử.`,
      status: 'active',
      image_keys: [],
      skus: [{ name: 'Tiêu chuẩn', price, stock, active: true }],
    });
  }

  product(id: string): Promise<Product> {
    return this.call<Product>('GET', `/api/products/${id}`);
  }

  async stock(productId: string): Promise<number> {
    return (await this.product(productId)).skus[0].stock;
  }

  async setCartItem(skuId: string, quantity: number): Promise<void> {
    await this.call('PUT', `/api/cart/items/${skuId}`, { quantity });
  }

  async orders(): Promise<Order[]> {
    return (await this.call<{ items: Order[] }>('GET', '/api/orders?limit=100')).items;
  }

  /** Status code of an authenticated GET, for asserting access denials. */
  async status(path: string): Promise<number> {
    const auth: Record<string, string> = this.token ? { Authorization: `Bearer ${this.token}` } : {};
    return (await this.ctx.fetch(path, { headers: auth })).status();
  }

  async checkout(method: 'COD' | 'VNPAY' | 'STRIPE'): Promise<{ id: string }> {
    const idempotencyKey = crypto.randomUUID();
    return this.call<{ id: string }>(
      'POST',
      '/api/checkout',
      { payment_method: method },
      { 'Idempotency-Key': idempotencyKey },
    );
  }

  async startPayment(checkoutId: string): Promise<{ payment_id: string; hosted_url: string }> {
    return this.call<{ payment_id: string; hosted_url: string }>('POST', `/api/checkout/${checkoutId}/start-payment`);
  }

  async fakeSubmit(paymentId: string, outcome: 'succeed' | 'fail'): Promise<string> {
    const res = await this.ctx.fetch(`/api/payments/fake/checkout/${paymentId}`, {
      method: 'POST',
      form: { outcome },
      maxRedirects: 0,
    });
    if (res.status() !== 302) throw new Error(`Expected 302, got ${res.status()}`);
    const location = res.headers()['location'];
    if (!location) throw new Error('No Location header in 302 response');
    return location;
  }

  async order(id: string): Promise<Order> {
    return this.call<Order>('GET', `/api/orders/${id}`);
  }

  async sellerAction(orderId: string, action: 'confirm' | 'ship' | 'deliver'): Promise<void> {
    await this.call('POST', `/api/seller/orders/${orderId}/${action}`);
  }

  async complete(orderId: string): Promise<void> {
    await this.call('POST', `/api/orders/${orderId}/complete`);
  }

  async wallet(): Promise<{ available: number; in_escrow: number; pending_payout: number }> {
    return this.call<{ available: number; in_escrow: number; pending_payout: number }>('GET', '/api/seller/wallet');
  }

  dispose(): Promise<void> {
    return this.ctx.dispose();
  }
}
