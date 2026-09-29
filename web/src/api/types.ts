// Aliases of generated OpenAPI types. Never declare request/response shapes by hand here.
import type { paths as CatalogPaths, components as CatalogComponents } from './gen/catalog';
import type { components as CommonComponents } from './gen/common';
import type { paths as IdentityPaths, components as IdentityComponents } from './gen/identity';
import type { paths as OrderPaths, components as OrderComponents } from './gen/order';

type CommonSchemas = CommonComponents['schemas'];
type IdentitySchemas = IdentityComponents['schemas'];
type CatalogSchemas = CatalogComponents['schemas'];
type OrderSchemas = OrderComponents['schemas'];

/** All gateway paths; the three specs never share a path. */
export type Paths = IdentityPaths & CatalogPaths & OrderPaths;

export type ErrorBody = CommonSchemas['Error'];
export type ErrorCode = CommonSchemas['ErrorCode'];
export type ShopRef = CommonSchemas['ShopRef'];

export type User = IdentitySchemas['User'];
export type Role = IdentitySchemas['Role'];
export type TokenResponse = IdentitySchemas['TokenResponse'];
export type RegisterRequest = IdentitySchemas['RegisterRequest'];
export type LoginRequest = IdentitySchemas['LoginRequest'];

export type Category = CatalogSchemas['Category'];
export type Shop = CatalogSchemas['Shop'];
export type ShopCreate = CatalogSchemas['ShopCreate'];
export type ShopUpdate = CatalogSchemas['ShopUpdate'];
export type ProductStatus = CatalogSchemas['ProductStatus'];
export type Sku = CatalogSchemas['Sku'];
export type SkuInput = CatalogSchemas['SkuInput'];
export type Product = CatalogSchemas['Product'];
export type ProductSummary = CatalogSchemas['ProductSummary'];
export type ProductSummaryPage = CatalogSchemas['ProductSummaryPage'];
export type ProductPage = CatalogSchemas['ProductPage'];
/** openapi-typescript cannot express the `required` list of ProductInput's allOf, so restore it here. */
export type ProductInput = CatalogSchemas['ProductInput'] &
  Required<Pick<CatalogSchemas['ProductPatch'], 'category_id' | 'name' | 'status' | 'skus'>>;
export type ProductPatch = CatalogSchemas['ProductPatch'];
export type UploadRequest = CatalogSchemas['UploadRequest'];
export type UploadResponse = CatalogSchemas['UploadResponse'];

export type Cart = OrderSchemas['Cart'];
export type CartShop = OrderSchemas['CartShop'];
export type CartItem = OrderSchemas['CartItem'];
export type CartItemUpdate = OrderSchemas['CartItemUpdate'];
export type Address = OrderSchemas['Address'];
export type CheckoutRequest = OrderSchemas['CheckoutRequest'];
export type CheckoutResponse = OrderSchemas['CheckoutResponse'];
export type OrderStatus = OrderSchemas['OrderStatus'];
export type ActorRole = OrderSchemas['ActorRole'];
export type Order = OrderSchemas['Order'];
export type OrderItem = OrderSchemas['OrderItem'];
export type OrderHistoryEntry = OrderSchemas['OrderHistoryEntry'];
export type OrderPage = OrderSchemas['OrderPage'];
export type CancelRequest = OrderSchemas['CancelRequest'];
export type OrderActionRequest = OrderSchemas['OrderActionRequest'];
