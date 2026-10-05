import type {
  Cart, CartItem, CartShop, Category, CheckoutRequest, CheckoutResponse, CreatePaymentRequest, LoginRequest, Order, OrderItem, OrderPage,
  OrderStatus, Paths, Product, ProductInput, ProductPage, ProductPatch, ProductStatus, ProductSummary, ProductSummaryPage,
  RegisterRequest, Role, Shop, ShopCreate, ShopRef, ShopUpdate, Sku, TokenResponse, UploadResponse, User,
  Payment, WalletEntry, Payout,
} from '@/api/types';
import { normalizeVi } from '@/lib/vi';
import { MockError, notFound, validation } from './mock-error';
import { paginate } from './paginate';
import { buildSeed } from './seed';

export const STORAGE_KEY = 'marketplace-mock-db:v2';
export const PAYMENT_TTL_MS = 15 * 60_000;
export const COMMISSION_RATE_BPS = 500;
export const STRIPE_MIN_AMOUNT = 10_000;
/** Stand-in for order-svc's SHIPPING_FEE_FLAT: reported per cart shop and charged per order. */
export const SHIPPING_FEE_FLAT = 30_000;
export const MOCK_ASSET_BASE = 'http://localhost:9000/product-images';
const RESERVED_SLUGS = ['me', 'new', 'admin', 'seller'];
const MAX_CART_LINES = 50;
const ADDRESS_FIELDS = ['full_name', 'phone', 'line1', 'ward', 'district', 'province'] as const;
const UPLOAD_EXTENSIONS: Record<string, string> = { 'image/jpeg': 'jpg', 'image/png': 'png', 'image/webp': 'webp' };

export type SearchQuery = NonNullable<Paths['/api/products']['get']['parameters']['query']>;
export type MockUser = User & { password: string };
export type MockProduct = {
  id: string; shop_id: string; category_id: string; name: string; description: string; status: ProductStatus;
  image_keys: string[]; skus: Sku[]; created_at: string; updated_at: string;
};
export type MockOrder = Order & { seller_user_id: string };
export type MockPayment = Payment & { buyer_id: string };
export type MockWalletEntry = WalletEntry & { seller_user_id: string };
export type MockPayout = Payout & { seller_user_id: string };
export type TokenGrant = { userId: string; roles: Role[] };
export type DbState = {
  users: MockUser[];
  shops: Shop[];
  categories: Category[];
  products: MockProduct[];
  carts: Record<string, Record<string, number>>;
  orders: MockOrder[];
  payments: MockPayment[];
  walletEntries: MockWalletEntry[];
  payouts: MockPayout[];
  keys: Record<string, { hash: string; id: string }>;
  idempotency: Record<string, { hash: string; response: CheckoutResponse }>;
  blobs: Record<string, string>;
  tokens: Record<string, TokenGrant>;
  /** Stand-in for the HttpOnly refresh cookie: the user of this browser's session. */
  session: string | null;
};
export type BuyerAction = 'cancel' | 'complete';
export type SellerAction = 'confirm' | 'ship' | 'deliver' | 'cancel';
type Actor = 'buyer' | 'seller';

// WS3 §5 state machine, per actor.
const TRANSITIONS: Record<Actor, Partial<Record<BuyerAction | SellerAction, { from: OrderStatus[]; to: OrderStatus }>>> = {
  buyer: {
    cancel: { from: ['PENDING_CONFIRMATION', 'AWAITING_PAYMENT'], to: 'CANCELLED' },
    complete: { from: ['DELIVERED'], to: 'COMPLETED' },
  },
  seller: {
    confirm: { from: ['PENDING_CONFIRMATION'], to: 'CONFIRMED' },
    ship: { from: ['CONFIRMED'], to: 'SHIPPING' },
    deliver: { from: ['SHIPPING'], to: 'DELIVERED' },
    cancel: { from: ['PENDING_CONFIRMATION', 'CONFIRMED'], to: 'CANCELLED' },
  },
};

const now = () => new Date().toISOString();
const uuid = () => crypto.randomUUID();
const newestFirst = (a: { created_at: string; id: string }, b: { created_at: string; id: string }) =>
  b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id);

function publicUser(u: MockUser): User {
  return { id: u.id, email: u.email, full_name: u.full_name, roles: [...u.roles], created_at: u.created_at };
}

function publicOrder(o: MockOrder): Order {
  const copy: Partial<MockOrder> = structuredClone(o);
  delete copy.seller_user_id;
  return copy as Order;
}

function fail(fields: Record<string, string>) {
  if (Object.keys(fields).length > 0) throw validation(fields);
}

export class MockDb {
  readonly state: DbState;

  constructor(private readonly storage?: Storage) {
    this.state = MockDb.load(storage) ?? buildSeed();
  }

  private static load(storage?: Storage): DbState | null {
    try {
      const raw = storage?.getItem(STORAGE_KEY);
      return raw ? (JSON.parse(raw) as DbState) : null;
    } catch {
      return null;
    }
  }

  private save(): void {
    try {
      this.storage?.setItem(STORAGE_KEY, JSON.stringify(this.state));
    } catch {
      // Storage full or unavailable: keep working in memory.
    }
  }

  // ---- identity ----

  register(input: RegisterRequest): User {
    const email = (input.email ?? '').trim().toLowerCase();
    const fullName = (input.full_name ?? '').trim();
    const fields: Record<string, string> = {};
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email) || email.length > 254) fields.email = 'must be a valid email';
    if (!input.password || input.password.length < 8 || input.password.length > 72) fields.password = 'must be 8-72 characters';
    if (!fullName || fullName.length > 100) fields.full_name = 'must be 1-100 characters';
    fail(fields);
    if (this.state.users.some((u) => u.email === email)) throw new MockError(409, 'CONFLICT', 'email already registered');
    const user: MockUser = { id: uuid(), email, password: input.password, full_name: fullName, roles: ['buyer'], created_at: now() };
    this.state.users.push(user);
    this.save();
    return publicUser(user);
  }

  login(input: LoginRequest): TokenResponse {
    const email = (input.email ?? '').trim().toLowerCase();
    const user = this.state.users.find((u) => u.email === email && u.password === input.password);
    if (!user) throw new MockError(401, 'UNAUTHENTICATED', 'invalid email or password');
    this.state.session = user.id;
    return this.issue(user);
  }

  refresh(): TokenResponse {
    const user = this.state.users.find((u) => u.id === this.state.session);
    if (!user) {
      this.state.session = null;
      this.save();
      throw new MockError(401, 'UNAUTHENTICATED', 'refresh token missing, expired or reused');
    }
    return this.issue(user);
  }

  logout(): void {
    this.state.session = null;
    this.save();
  }

  private issue(user: MockUser): TokenResponse {
    const token = `mock-at.${uuid()}`;
    // Roles are a snapshot, like JWT claims: a new role needs a new token (POST /api/auth/refresh).
    this.state.tokens[token] = { userId: user.id, roles: [...user.roles] };
    this.save();
    return { access_token: token, token_type: 'Bearer', expires_in: 900, user: publicUser(user) };
  }

  grant(authorization: string | null): TokenGrant | undefined {
    const token = authorization?.match(/^Bearer (.+)$/)?.[1];
    return token ? this.state.tokens[token] : undefined;
  }

  requireGrant(authorization: string | null, role?: Role): TokenGrant {
    const grant = this.grant(authorization);
    if (!grant) throw new MockError(401, 'UNAUTHENTICATED', 'missing or invalid access token');
    if (role && !grant.roles.includes(role)) throw new MockError(403, 'FORBIDDEN', `role ${role} required`);
    return grant;
  }

  me(userId: string): User {
    const user = this.state.users.find((u) => u.id === userId);
    if (!user) throw new MockError(401, 'UNAUTHENTICATED', 'unknown user');
    return publicUser(user);
  }

  // ---- catalog ----

  categories(): Category[] {
    return structuredClone(this.state.categories);
  }

  private allCategories(): Category[] {
    return this.state.categories.flatMap((c) => [c, ...c.children]);
  }

  private shopOf(p: MockProduct): Shop {
    return this.state.shops.find((s) => s.id === p.shop_id)!;
  }

  private shopRef(shop: Shop): ShopRef {
    return { id: shop.id, name: shop.name, slug: shop.slug };
  }

  private minPrice(p: MockProduct): number | null {
    const prices = p.skus.filter((s) => s.active).map((s) => s.price);
    return prices.length ? Math.min(...prices) : null;
  }

  imageUrl(key: string): string {
    if (this.state.blobs[key]) return this.state.blobs[key];
    if (key.startsWith('seed/')) return new URL(`/mock-images/${key.slice('seed/'.length)}`, globalThis.location?.origin ?? 'http://localhost').href;
    return `${MOCK_ASSET_BASE}/${key}`;
  }

  private toProduct(p: MockProduct): Product {
    return {
      id: p.id, shop: this.shopRef(this.shopOf(p)), category_id: p.category_id, name: p.name, description: p.description,
      status: p.status, images: p.image_keys.map((key) => ({ object_key: key, url: this.imageUrl(key) })),
      skus: p.skus.map((s) => ({ ...s })), min_price: this.minPrice(p), created_at: p.created_at, updated_at: p.updated_at,
    };
  }

  private toSummary(p: MockProduct): ProductSummary {
    return {
      id: p.id, name: p.name, image_url: p.image_keys[0] ? this.imageUrl(p.image_keys[0]) : null,
      min_price: this.minPrice(p) ?? 0, shop: this.shopRef(this.shopOf(p)),
    };
  }

  search(query: SearchQuery): ProductSummaryPage {
    for (const key of ['min_price', 'max_price'] as const) {
      const value = query[key];
      if (value != null && (!Number.isInteger(value) || value < 0)) throw validation({ [key]: 'must be a non-negative integer' });
    }
    let items = this.state.products.filter((p) => p.status === 'active' && p.skus.some((s) => s.active));
    if (query.category) {
      const root = this.allCategories().find((c) => c.slug === query.category);
      const ids = new Set(root ? [root.id, ...root.children.map((c) => c.id)] : []);
      items = items.filter((p) => ids.has(p.category_id));
    }
    if (query.shop) {
      const shop = this.state.shops.find((s) => s.slug === query.shop);
      items = shop ? items.filter((p) => p.shop_id === shop.id) : [];
    }
    const price = (p: MockProduct) => this.minPrice(p) ?? 0;
    if (query.min_price != null) items = items.filter((p) => price(p) >= query.min_price!);
    if (query.max_price != null) items = items.filter((p) => price(p) <= query.max_price!);
    const terms = query.q ? normalizeVi(query.q).split(/\s+/).filter(Boolean) : [];
    if (terms.length) {
      items = items.filter((p) => {
        const haystack = normalizeVi(`${p.name} ${p.description}`);
        return terms.every((t) => haystack.includes(t));
      });
    }
    const score = (p: MockProduct) => terms.filter((t) => normalizeVi(p.name).includes(t)).length;
    const sort = query.sort ?? (terms.length ? 'relevance' : 'newest');
    const compare: Record<typeof sort, (a: MockProduct, b: MockProduct) => number> = {
      newest: newestFirst,
      relevance: (a, b) => score(b) - score(a) || newestFirst(a, b),
      price_asc: (a, b) => price(a) - price(b) || newestFirst(a, b),
      price_desc: (a, b) => price(b) - price(a) || newestFirst(a, b),
    };
    return paginate([...items].sort(compare[sort]).map((p) => this.toSummary(p)), query.limit, query.cursor);
  }

  product(id: string, viewerId?: string): Product {
    const p = this.state.products.find((x) => x.id === id);
    if (!p || (p.status !== 'active' && this.shopOf(p).owner_user_id !== viewerId)) throw notFound('product');
    return this.toProduct(p);
  }

  shopBySlug(slug: string): Shop {
    const shop = this.state.shops.find((s) => s.slug === slug);
    if (!shop) throw notFound('shop');
    return { ...shop };
  }

  createShop(userId: string, input: ShopCreate): Shop {
    const name = (input.name ?? '').trim();
    const slug = input.slug ?? '';
    const fields: Record<string, string> = {};
    if (name.length < 3 || name.length > 80) fields.name = 'must be 3-80 characters';
    if (!/^[a-z0-9-]{3,50}$/.test(slug) || RESERVED_SLUGS.includes(slug)) fields.slug = 'must match ^[a-z0-9-]{3,50}$ and not be reserved';
    fail(fields);
    if (this.state.shops.some((s) => s.owner_user_id === userId)) throw new MockError(409, 'CONFLICT', 'user already has a shop');
    if (this.state.shops.some((s) => s.slug === slug)) throw new MockError(409, 'CONFLICT', 'slug already taken');
    const shop: Shop = { id: uuid(), name, slug, description: input.description ?? '', owner_user_id: userId, created_at: now() };
    this.state.shops.push(shop);
    const user = this.state.users.find((u) => u.id === userId);
    if (user && !user.roles.includes('seller')) user.roles.push('seller');
    this.save();
    return { ...shop };
  }

  private ownShop(userId: string): Shop {
    const shop = this.state.shops.find((s) => s.owner_user_id === userId);
    if (!shop) throw notFound('shop');
    return shop;
  }

  myShop(userId: string): Shop {
    return { ...this.ownShop(userId) };
  }

  updateMyShop(userId: string, input: ShopUpdate): Shop {
    const shop = this.ownShop(userId);
    if (Object.keys(input).length === 0) throw validation({ body: 'at least one field is required' });
    if (input.name !== undefined) {
      const name = input.name.trim();
      if (name.length < 3 || name.length > 80) throw validation({ name: 'must be 3-80 characters' });
      shop.name = name;
    }
    if (input.description !== undefined) shop.description = input.description;
    this.save();
    return { ...shop };
  }

  sellerProducts(userId: string, status?: ProductStatus | null, limit?: number | null, cursor?: string | null): ProductPage {
    const shop = this.ownShop(userId);
    const items = this.state.products.filter((p) => p.shop_id === shop.id && (!status || p.status === status)).sort(newestFirst);
    return paginate(items.map((p) => this.toProduct(p)), limit, cursor);
  }

  private validateProduct(shopId: string, input: ProductPatch, requireAll: boolean): void {
    const fields: Record<string, string> = {};
    if (requireAll || input.category_id !== undefined) {
      if (!this.allCategories().some((c) => c.id === input.category_id)) fields.category_id = 'unknown category';
    }
    if (requireAll || input.name !== undefined) {
      const name = (input.name ?? '').trim();
      if (name.length < 5 || name.length > 200) fields.name = 'must be 5-200 characters';
    }
    if (requireAll && !input.status) fields.status = 'required';
    if (input.image_keys) {
      if (input.image_keys.length > 9) fields.image_keys = 'at most 9 images';
      input.image_keys.forEach((key, i) => {
        if (!key.startsWith(`shops/${shopId}/`) && !key.startsWith('seed/')) fields[`image_keys[${i}]`] = 'must be an upload of your shop';
      });
    }
    if (requireAll || input.skus !== undefined) {
      const skus = input.skus ?? [];
      if (skus.length < 1 || skus.length > 50) fields.skus = 'must have 1-50 SKUs';
      skus.forEach((s, i) => {
        if (!s.name?.trim()) fields[`skus[${i}].name`] = 'required';
        if (!Number.isInteger(s.price) || s.price < 1000 || s.price > 1_000_000_000) fields[`skus[${i}].price`] = 'must be 1000-1000000000';
        if (!Number.isInteger(s.stock) || s.stock < 0 || s.stock > 100_000) fields[`skus[${i}].stock`] = 'must be 0-100000';
        if (requireAll || !s.id) {
          if (s.expected_stock !== undefined) fields[`skus[${i}].expected_stock`] = 'must not be set for a new SKU';
        } else if (!Number.isInteger(s.expected_stock) || s.expected_stock! < 0) {
          fields[`skus[${i}].expected_stock`] = 'is required when id is set';
        }
      });
    }
    fail(fields);
  }

  createProduct(userId: string, input: ProductInput): Product {
    const shop = this.ownShop(userId);
    this.validateProduct(shop.id, input, true);
    const ts = now();
    const p: MockProduct = {
      id: uuid(), shop_id: shop.id, category_id: input.category_id, name: input.name.trim(), description: input.description ?? '',
      status: input.status, image_keys: input.image_keys ?? [],
      skus: input.skus.map((s) => ({ id: uuid(), name: s.name.trim(), price: s.price, stock: s.stock, active: s.active ?? true })),
      created_at: ts, updated_at: ts,
    };
    this.state.products.push(p);
    this.save();
    return this.toProduct(p);
  }

  updateProduct(userId: string, id: string, input: ProductPatch): Product {
    const shop = this.ownShop(userId);
    const p = this.state.products.find((x) => x.id === id && x.shop_id === shop.id);
    if (!p) throw notFound('product');
    if (Object.keys(input).length === 0) throw validation({ body: 'at least one field is required' });
    this.validateProduct(shop.id, input, false);
    if (input.skus !== undefined) {
      const stale = input.skus.flatMap((s) => {
        const current = p.skus.find((x) => x.id === s.id);
        return current && s.expected_stock !== current.stock ? [{ sku_id: current.id, expected_stock: s.expected_stock, stock: current.stock }] : [];
      });
      if (stale.length > 0) throw new MockError(409, 'CONFLICT', 'stock changed since the product was loaded; reload and retry', { skus: stale });
    }
    if (input.category_id !== undefined) p.category_id = input.category_id;
    if (input.name !== undefined) p.name = input.name.trim();
    if (input.description !== undefined) p.description = input.description;
    if (input.status !== undefined) p.status = input.status;
    if (input.image_keys !== undefined) p.image_keys = [...input.image_keys];
    if (input.skus !== undefined) {
      const next = input.skus.map((s, i) => {
        const existing = s.id ? p.skus.find((x) => x.id === s.id) : undefined;
        if (s.id && !existing) throw validation({ [`skus[${i}].id`]: 'unknown SKU' });
        return { id: existing?.id ?? uuid(), name: s.name.trim(), price: s.price, stock: s.stock, active: s.active ?? true };
      });
      const kept = new Set(next.map((s) => s.id));
      // Omitted SKUs are deactivated, never deleted (old orders reference them).
      p.skus = [...next, ...p.skus.filter((s) => !kept.has(s.id)).map((s) => ({ ...s, active: false }))];
    }
    p.updated_at = now();
    this.save();
    return this.toProduct(p);
  }

  archiveProduct(userId: string, id: string): void {
    const shop = this.ownShop(userId);
    const p = this.state.products.find((x) => x.id === id && x.shop_id === shop.id);
    if (!p) throw notFound('product');
    p.status = 'archived';
    p.updated_at = now();
    this.save();
  }

  createUpload(userId: string, contentType: string): UploadResponse {
    const shop = this.ownShop(userId);
    const ext = UPLOAD_EXTENSIONS[contentType];
    if (!ext) throw validation({ content_type: 'must be image/jpeg, image/png or image/webp' });
    const key = `shops/${shop.id}/${uuid()}.${ext}`;
    return {
      upload_url: MOCK_ASSET_BASE,
      fields: { key, 'Content-Type': contentType, policy: 'mock-policy', 'x-amz-signature': 'mock-signature' },
      object_key: key,
      public_url: `${MOCK_ASSET_BASE}/${key}`,
    };
  }

  storeBlob(key: string, dataUrl: string): void {
    this.state.blobs[key] = dataUrl;
    this.save();
  }

  // ---- cart & orders ----

  private skuContext(skuId: string): { sku: Sku; product: MockProduct; shop: Shop } | undefined {
    for (const product of this.state.products) {
      const sku = product.skus.find((s) => s.id === skuId);
      if (sku) return { sku, product, shop: this.shopOf(product) };
    }
    return undefined;
  }

  private firstImage(p: MockProduct): string | null {
    return p.image_keys[0] ? this.imageUrl(p.image_keys[0]) : null;
  }

  cart(userId: string): Cart {
    const groups = new Map<string, CartShop>();
    let subtotal = 0;
    for (const [skuId, quantity] of Object.entries(this.state.carts[userId] ?? {})) {
      const ctx = this.skuContext(skuId);
      if (!ctx) continue;
      const available = ctx.sku.active && ctx.product.status === 'active';
      const item: CartItem = {
        sku_id: skuId, product_id: ctx.product.id, product_name: ctx.product.name, variant_name: ctx.sku.name,
        image_url: this.firstImage(ctx.product), unit_price: ctx.sku.price, quantity, stock: ctx.sku.stock, available,
      };
      if (available) subtotal += item.unit_price * quantity;
      const group = groups.get(ctx.shop.id) ?? { shop: this.shopRef(ctx.shop), items: [], shipping_fee: SHIPPING_FEE_FLAT };
      group.items.push(item);
      groups.set(ctx.shop.id, group);
    }
    return { shops: [...groups.values()], subtotal };
  }

  setCartItem(userId: string, skuId: string, quantity: number): Cart {
    if (!Number.isInteger(quantity) || quantity < 1 || quantity > 99) throw validation({ quantity: 'must be 1-99' });
    const ctx = this.skuContext(skuId);
    if (!ctx) throw notFound('sku');
    if (!ctx.sku.active || ctx.product.status !== 'active') throw new MockError(409, 'CONFLICT', 'SKU is not available');
    if (ctx.shop.owner_user_id === userId) throw new MockError(409, 'CONFLICT', 'cannot buy from your own shop');
    const lines = (this.state.carts[userId] ??= {});
    if (!(skuId in lines) && Object.keys(lines).length >= MAX_CART_LINES) throw new MockError(409, 'CONFLICT', 'cart is full');
    lines[skuId] = quantity;
    this.save();
    return this.cart(userId);
  }

  removeCartItem(userId: string, skuId: string): Cart {
    delete this.state.carts[userId]?.[skuId];
    this.save();
    return this.cart(userId);
  }

  checkout(userId: string, key: string, body: CheckoutRequest): CheckoutResponse {
    if (!key || key.length > 100) throw validation({ 'Idempotency-Key': 'required, 1-100 characters' });
    const idemKey = `${userId}:${key}`;
    const hash = JSON.stringify(body);
    const previous = this.state.idempotency[idemKey];
    if (previous) {
      if (previous.hash !== hash) throw new MockError(422, 'IDEMPOTENCY_KEY_REUSED', 'Idempotency-Key reused with a different body');
      return structuredClone(previous.response);
    }

    const fields: Record<string, string> = {};
    for (const f of ADDRESS_FIELDS) if (!body.shipping_address?.[f]?.trim()) fields[`shipping_address.${f}`] = 'required';
    if (!['COD', 'VNPAY', 'STRIPE'].includes(body.payment_method ?? '')) fields.payment_method = 'must be COD, VNPAY or STRIPE';
    fail(fields);

    const lines = this.state.carts[userId] ?? {};
    const selected = (body.sku_ids ?? Object.keys(lines)).filter((id) => id in lines);
    if (selected.length === 0) throw validation({ sku_ids: 'no cart lines selected' });
    const resolved = selected.map((skuId) => ({ skuId, quantity: lines[skuId], ctx: this.skuContext(skuId) }));
    const unavailable = resolved
      .filter(({ ctx }) => !ctx || !ctx.sku.active || ctx.product.status !== 'active' || ctx.shop.owner_user_id === userId)
      .map((r) => r.skuId);
    if (unavailable.length) throw new MockError(409, 'CONFLICT', 'some cart lines are unavailable', { sku_ids: unavailable });
    const shortages = resolved
      .filter(({ quantity, ctx }) => quantity > ctx!.sku.stock)
      .map(({ skuId, quantity, ctx }) => ({ sku_id: skuId, requested: quantity, available: ctx!.sku.stock }));
    if (shortages.length) throw new MockError(409, 'OUT_OF_STOCK', 'insufficient stock', { shortages });

    // All-or-nothing: every line is reservable, so reserve all and create one order per shop.
    const checkoutId = uuid();
    const ts = now();
    const byShop = new Map<string, typeof resolved>();
    for (const r of resolved) {
      r.ctx!.sku.stock -= r.quantity;
      byShop.set(r.ctx!.shop.id, [...(byShop.get(r.ctx!.shop.id) ?? []), r]);
    }
    const isOnlinePayment = body.payment_method !== 'COD';
    const orders: MockOrder[] = [...byShop.values()].map((group) => {
      const shop = group[0].ctx!.shop;
      const items: OrderItem[] = group.map(({ skuId, quantity, ctx }) => ({
        sku_id: skuId, product_id: ctx!.product.id, product_name: ctx!.product.name, variant_name: ctx!.sku.name,
        image_url: this.firstImage(ctx!.product), unit_price: ctx!.sku.price, quantity, line_total: ctx!.sku.price * quantity,
      }));
      const subtotal = items.reduce((sum, i) => sum + i.line_total, 0);
      const commissionAmount = isOnlinePayment ? Math.floor(subtotal * COMMISSION_RATE_BPS / 10_000) : 0;
      return {
        id: uuid(), checkout_id: checkoutId, status: isOnlinePayment ? 'AWAITING_PAYMENT' : 'PENDING_CONFIRMATION', buyer_id: userId, seller_user_id: shop.owner_user_id,
        shop: { id: shop.id, name: shop.name, slug: shop.slug }, items, subtotal, shipping_fee: SHIPPING_FEE_FLAT, total: subtotal + SHIPPING_FEE_FLAT,
        shipping_address: { ...body.shipping_address }, payment_method: body.payment_method, cancel_reason: null,
        // COD: payment_status NONE, no deadline, no commission. Online: UNPAID, deadline, commission.
        payment_status: isOnlinePayment ? 'UNPAID' : 'NONE',
        payment_deadline: isOnlinePayment ? new Date(Date.now() + PAYMENT_TTL_MS).toISOString() : null,
        commission_rate_bps: isOnlinePayment ? COMMISSION_RATE_BPS : 0,
        commission_amount: commissionAmount,
        delivered_at: null,
        history: isOnlinePayment
          ? [
              { from: null, to: 'PENDING', actor_role: 'system', note: null, created_at: ts },
              { from: 'PENDING', to: 'AWAITING_PAYMENT', actor_role: 'system', note: null, created_at: ts },
            ]
          : [
              { from: null, to: 'PENDING', actor_role: 'system', note: null, created_at: ts },
              { from: 'PENDING', to: 'PENDING_CONFIRMATION', actor_role: 'system', note: null, created_at: ts },
            ],
        created_at: ts, updated_at: ts,
      };
    });
    this.state.orders.push(...orders);
    for (const r of resolved) delete lines[r.skuId];
    const response: CheckoutResponse = { checkout_id: checkoutId, orders: orders.map(publicOrder) };
    this.state.idempotency[idemKey] = { hash, response };
    this.save();
    return structuredClone(response);
  }

  buyerOrders(userId: string, status?: OrderStatus | null, limit?: number | null, cursor?: string | null): OrderPage {
    const items = this.state.orders.filter((o) => o.buyer_id === userId && (!status || o.status === status)).sort(newestFirst);
    return paginate(items.map(publicOrder), limit, cursor);
  }

  sellerOrders(userId: string, status?: OrderStatus | null, limit?: number | null, cursor?: string | null): OrderPage {
    const items = this.state.orders.filter((o) => o.seller_user_id === userId && (!status || o.status === status)).sort(newestFirst);
    return paginate(items.map(publicOrder), limit, cursor);
  }

  order(userId: string, id: string): Order {
    const o = this.state.orders.find((x) => x.id === id && (x.buyer_id === userId || x.seller_user_id === userId));
    if (!o) throw notFound('order');
    return publicOrder(o);
  }

  buyerAction(userId: string, id: string, action: BuyerAction, reason?: string): Order {
    return this.transition((o) => o.buyer_id === userId, 'buyer', id, action, reason);
  }

  sellerAction(userId: string, id: string, action: SellerAction, reason?: string): Order {
    return this.transition((o) => o.seller_user_id === userId, 'seller', id, action, reason);
  }

  private transition(owns: (o: MockOrder) => boolean, actor: Actor, id: string, action: BuyerAction | SellerAction, reason?: string): Order {
    const order = this.state.orders.find((o) => o.id === id && owns(o));
    if (!order) throw notFound('order');
    const note = reason?.trim() ?? '';
    if (action === 'cancel' && (note.length < 1 || note.length > 200)) throw validation({ reason: 'must be 1-200 characters' });
    if (note.length > 200) throw validation({ reason: 'at most 200 characters' });
    const rule = TRANSITIONS[actor][action];
    if (!rule || !rule.from.includes(order.status)) {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', `cannot ${action} an order in ${order.status}`);
    }
    const from = order.status;
    order.status = rule.to;
    order.updated_at = now();
    if (rule.to === 'CANCELLED') {
      order.cancel_reason = note;
      for (const item of order.items) {
        const ctx = this.skuContext(item.sku_id);
        if (ctx) ctx.sku.stock += item.quantity; // catalog releases the reservation
      }
      // When a buyer cancels an AWAITING_PAYMENT order, cancel all its siblings.
      if (actor === 'buyer' && from === 'AWAITING_PAYMENT') {
        const siblings = this.state.orders.filter(
          (o) => o.checkout_id === order.checkout_id && o.id !== order.id && o.buyer_id === order.buyer_id && o.status === 'AWAITING_PAYMENT',
        );
        for (const sibling of siblings) {
          sibling.status = 'CANCELLED';
          sibling.cancel_reason = note;
          sibling.updated_at = order.updated_at;
          for (const item of sibling.items) {
            const ctx = this.skuContext(item.sku_id);
            if (ctx) ctx.sku.stock += item.quantity;
          }
          sibling.history.push({ from: 'AWAITING_PAYMENT', to: 'CANCELLED', actor_role: 'system', note: note || null, created_at: sibling.updated_at });
        }
      }
    }
    order.history.push({ from, to: rule.to, actor_role: actor, note: note || null, created_at: order.updated_at });
    this.save();
    return publicOrder(order);
  }

  // ---- payments ----

  private publicPayment(p: MockPayment): Payment {
    const copy: Partial<MockPayment> = structuredClone(p);
    delete copy.buyer_id;
    return copy as Payment;
  }

  createPayment(userId: string, key: string, body: CreatePaymentRequest): Payment {
    // Rule 1: key validation
    if (!key || key.length > 100) throw validation({ 'Idempotency-Key': 'required, 1-100 characters' });

    // Rule 2: check for replayed idempotency key
    const paymentKey = `payment:${userId}:${key}`;
    const existingKey = this.state.keys[paymentKey];
    if (existingKey) {
      const existingPayment = this.state.payments.find((p) => p.id === existingKey.id);
      if (existingPayment && existingPayment.checkout_id === body.checkout_id) {
        return this.publicPayment(existingPayment);
      }
      throw new MockError(422, 'IDEMPOTENCY_KEY_REUSED', 'Idempotency-Key reused with a different checkout_id');
    }

    // Rule 3: order must exist
    const orders = this.state.orders.filter((o) => o.buyer_id === userId && o.checkout_id === body.checkout_id);
    if (orders.length === 0) throw notFound('checkout');

    const checkout = orders[0];

    // Rule 4: payment_method must not be COD
    if (checkout.payment_method === 'COD') {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', 'cannot create payment for COD checkout', { reason: 'COD_CHECKOUT' });
    }

    // Rule 5: no SUCCEEDED payment yet
    if (this.state.payments.some((p) => p.checkout_id === body.checkout_id && p.status === 'SUCCEEDED')) {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', 'payment already succeeded for this checkout', { reason: 'ALREADY_PAID' });
    }

    // Rule 6: at least one order must be AWAITING_PAYMENT
    if (!orders.some((o) => o.status === 'AWAITING_PAYMENT')) {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', 'no order is awaiting payment', { reason: 'NOT_AWAITING_PAYMENT' });
    }

    // Rule 7: deadline not passed
    const deadline = new Date(checkout.payment_deadline!).getTime();
    if (Date.now() >= deadline) {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', 'payment deadline has passed', { reason: 'DEADLINE_PASSED' });
    }

    // Rule 8: amount check for STRIPE
    const amount = orders.reduce((sum, o) => sum + o.total, 0);
    if (checkout.payment_method === 'STRIPE' && amount < STRIPE_MIN_AMOUNT) {
      throw new MockError(409, 'INVALID_STATE_TRANSITION', `amount must be at least ${STRIPE_MIN_AMOUNT}`, { reason: 'BELOW_PROVIDER_MINIMUM' });
    }

    // Expire any PENDING payment for this checkout
    for (const p of this.state.payments.filter((p) => p.checkout_id === body.checkout_id && p.status === 'PENDING')) {
      p.status = 'EXPIRED';
      p.redirect_url = null;
      p.updated_at = now();
    }

    // Create the new payment
    const paymentId = uuid();
    const origin = typeof globalThis !== 'undefined' && globalThis.location?.origin ? globalThis.location.origin : 'http://localhost';
    const payment: MockPayment = {
      id: paymentId,
      checkout_id: body.checkout_id,
      method: checkout.payment_method as 'VNPAY' | 'STRIPE',
      provider: 'fake',
      status: 'PENDING',
      amount,
      redirect_url: `${origin}/__mock/pay/${paymentId}`,
      expires_at: checkout.payment_deadline!,
      failure_reason: null,
      created_at: now(),
      updated_at: now(),
      buyer_id: userId,
    };
    this.state.payments.push(payment);
    this.state.keys[paymentKey] = { hash: JSON.stringify(body), id: paymentId };
    this.save();
    return this.publicPayment(payment);
  }

  payment(userId: string, id: string): Payment {
    const p = this.state.payments.find((x) => x.id === id && x.buyer_id === userId);
    if (!p) throw notFound('payment');

    // Expire PENDING payments past their deadline
    if (p.status === 'PENDING' && Date.now() >= new Date(p.expires_at).getTime()) {
      p.status = 'EXPIRED';
      p.redirect_url = null;
      p.updated_at = now();
      this.save();
    }

    return this.publicPayment(p);
  }

  submitMockPayment(id: string, outcome: 'succeed' | 'fail'): void {
    const payment = this.state.payments.find((p) => p.id === id);
    if (!payment) throw notFound('payment');

    // No-op if already terminal
    if (payment.status !== 'PENDING') return;

    if (outcome === 'fail') {
      payment.status = 'FAILED';
      payment.failure_reason = 'mock_declined';
      payment.redirect_url = null;
      payment.updated_at = now();
    } else {
      // Check for duplicate
      const existingSucceeded = this.state.payments.find((p) => p.checkout_id === payment.checkout_id && p.status === 'SUCCEEDED');
      if (existingSucceeded) {
        payment.status = 'DUPLICATE';
        payment.redirect_url = null;
        payment.updated_at = now();
      } else {
        payment.status = 'SUCCEEDED';
        payment.redirect_url = null;
        payment.updated_at = now();

        // Transition orders to PENDING_CONFIRMATION with payment_status PAID
        const orders = this.state.orders.filter((o) => o.checkout_id === payment.checkout_id && o.status === 'AWAITING_PAYMENT');
        for (const order of orders) {
          order.status = 'PENDING_CONFIRMATION';
          order.payment_status = 'PAID';
          order.updated_at = now();
          order.history.push({ from: 'AWAITING_PAYMENT', to: 'PENDING_CONFIRMATION', actor_role: 'system', note: null, created_at: order.updated_at });
        }

        // Handle late captures (orders already cancelled)
        const cancelled = this.state.orders.filter((o) => o.checkout_id === payment.checkout_id && o.status === 'CANCELLED');
        for (const order of cancelled) {
          order.payment_status = 'REFUNDED';
        }
      }
    }
    this.save();
  }
}
