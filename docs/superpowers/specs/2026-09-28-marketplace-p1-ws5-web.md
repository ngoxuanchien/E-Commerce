# WS5 — web (React)

> Depends on: WS0 (`api/openapi/*.yaml`). Owns: `web/`. During development all API calls are served by **MSW** handlers typed from the OpenAPI specs, so no backend is needed.

## 1. Responsibility

The buyer storefront and the seller center (`/seller`) as one single-page app. UI copy is Vietnamese; code, comments and identifiers are English.

## 2. Stack

Vite + React + TypeScript (strict), React Router, TanStack Query, `openapi-typescript` + `openapi-fetch` (types generated from `api/openapi/`, `npm run gen:api`), React Hook Form + Zod, Tailwind CSS + shadcn/ui components, MSW for mocks, Vitest + Testing Library, Playwright. Prices formatted with `Intl.NumberFormat('vi-VN', {style:'currency', currency:'VND'})`.

## 3. Pages

**Buyer**
| Route | Content |
|---|---|
| `/` | category list + newest products grid |
| `/search?q=&category=&min_price=&max_price=&sort=` | results with filters, "load more" via cursor |
| `/products/:id` | image gallery, variant (SKU) selector, stock, quantity, add to cart, shop link |
| `/shops/:slug` | shop header + its products |
| `/cart` | lines grouped by shop, quantity edit/remove, unavailable lines flagged, per-shop selection, subtotal |
| `/checkout` | address form, order preview per shop (subtotal + shipping), COD, place order |
| `/orders`, `/orders/:id` | order list with status tabs; detail with history timeline, cancel / confirm received |
| `/login`, `/register` | auth forms |

**Seller** (`/seller`, requires `seller` role; users without a shop see "Open your shop")
| Route | Content |
|---|---|
| `/seller/open-shop` | create shop form → on success call `/api/auth/refresh` to receive the seller role |
| `/seller/products` | table with status filter |
| `/seller/products/new`, `/seller/products/:id` | product form: category, name, description, images (presigned upload with progress, reorder, max 9), SKU rows (name, price, stock, active), status |
| `/seller/orders` | orders by status tabs; actions confirm / ship / deliver / cancel (with reason) |
| `/seller/shop` | edit shop profile |

## 4. Cross-cutting behavior

- **Auth:** access token kept in memory only. On app load call `POST /api/auth/refresh` (cookie) to restore the session. The API client attaches `Authorization`; on 401 it performs a single-flight refresh and retries once; refresh failure → clear session, redirect to `/login?next=...`. Route guards: `RequireAuth`, `RequireSeller`.
- **Checkout idempotency:** generate an `Idempotency-Key` (UUID) when the checkout page mounts; reuse it for retries/double-clicks; regenerate after success. Disable the button while submitting.
- **Errors:** map API `error.code` to Vietnamese messages (`OUT_OF_STOCK` → show affected lines in the cart; `RATE_LIMITED`, `UPSTREAM_UNAVAILABLE` → toast with retry). Field `details` from `VALIDATION_ERROR` map onto form fields.
- **Server state** is only in TanStack Query (query keys per resource; cart/order mutations invalidate the relevant queries). No global client store beyond the auth context.
- **Dev proxy:** Vite proxies `/api` → `http://localhost:8080` so the refresh cookie is same-origin. `VITE_API_MOCK=true` enables MSW instead.
- **Accessibility:** semantic landmarks, labelled form controls, keyboard-operable variant selector and dialogs.
- **Responsive:** mobile-first; usable from 360 px width.

## 5. Structure

```
web/src/api        generated schema types, typed client, query hooks per resource
web/src/mocks      MSW handlers + in-memory fixtures (shops, products, cart, orders) mirroring server rules
web/src/features   auth, catalog, cart, checkout, orders, seller-products, seller-orders, shop
web/src/components shared UI (layout, price, pagination, image uploader, status badge)
web/src/routes     router + guards
web/e2e            Playwright specs (MSW mode)
```

## 6. Testing

- Unit/component (Vitest + Testing Library): price formatting, cart grouping, product form SKU editing & validation, auth refresh single-flight, error-code mapping.
- Playwright against MSW mode: register → login → search → add from 2 shops → checkout → see 2 orders; seller opens shop → creates product with image → confirms/ships/delivers an order; out-of-stock checkout shows the affected line.
- `npm run lint` (ESLint + TypeScript `--noEmit`) clean.

## 7. Acceptance criteria

- [ ] All pages in §3 work end-to-end in MSW mode.
- [ ] Generated API types compile against the current OpenAPI; no hand-written request/response types.
- [ ] Session survives a page reload (via refresh cookie) when running against the real gateway.
- [ ] Lint, unit and Playwright suites green.
