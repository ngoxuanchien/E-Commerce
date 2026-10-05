import type { OrderStatus, PayoutStatus, ProductStatus } from './types';

export type ProductQuery = {
  q?: string;
  category?: string;
  shop?: string;
  min_price?: number;
  max_price?: number;
  sort?: 'relevance' | 'newest' | 'price_asc' | 'price_desc';
};

export const qk = {
  categories: ['categories'] as const,
  products: (query: ProductQuery) => ['products', query] as const,
  product: (id: string) => ['product', id] as const,
  shop: (slug: string) => ['shop', slug] as const,
  myShop: ['my-shop'] as const,
  cart: ['cart'] as const,
  orders: (status?: OrderStatus) => ['orders', status ?? 'all'] as const,
  order: (id: string) => ['order', id] as const,
  sellerOrders: (status?: OrderStatus) => ['seller-orders', status ?? 'all'] as const,
  sellerProducts: (status?: ProductStatus) => ['seller-products', status ?? 'all'] as const,
  payment: (id: string) => ['payment', id] as const,
  wallet: ['wallet'] as const,
  walletEntries: ['wallet-entries'] as const,
  payouts: (status?: PayoutStatus) => ['payouts', status ?? 'all'] as const,
};
