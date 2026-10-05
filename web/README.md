# Chợ Phiên web (WS5)

The buyer storefront and the seller center (`/seller`) of the marketplace, as one React SPA.
UI copy is Vietnamese; code and docs are English.

Stack: Vite, React 19, TypeScript (strict), React Router, TanStack Query, openapi-typescript +
openapi-fetch, React Hook Form + Zod, Tailwind CSS 4 + shadcn/ui, MSW, Vitest + Testing Library,
Playwright.

## Prerequisites

Node 24 or newer. Run `npm ci` once. For Playwright, also run `npx playwright install chromium` once.

## Scripts

| Script | What it does |
|---|---|
| `npm run dev` | Dev server; `/api` is proxied to `VITE_API_PROXY_TARGET` (default `http://localhost:8080`, the gateway) |
| `npm run dev:mock` | Dev server with every API call served by MSW mocks (no backend needed) |
| `npm run build` | Type-check and build into `dist/` |
| `npm run lint` | Generated-types drift check + ESLint + `tsc -b` |
| `npm test` | Unit and component tests (Vitest, non-watch) |
| `npm run e2e` | Playwright suites (starts the dev servers itself) |
| `npm run gen:api` | Regenerate `src/api/gen/*` from `../api/openapi/*.yaml` |
| `npm run gen:api:check` | Fail if `src/api/gen/*` is out of date |

CI runs `npm ci && npm run lint && npm test`. Run the Playwright suites locally.

## Mock mode

`npm run dev:mock` starts an MSW service worker backed by an in-memory database
(`src/mocks/db.ts`). The database follows the catalog and order service rules: one order per shop,
a flat 30,000 ₫ shipping fee (`SHIPPING_FEE_FLAT` in `mocks/db.ts`; checkout shows the server's `CartShop.shipping_fee`), stock reserved at checkout and returned on cancel, the order state
machine, idempotent checkout, and a seller role that appears only after refreshing the session.

Seeded accounts (password `matkhau123`):

| Email | Role |
|---|---|
| `an@example.com` | buyer, with one delivered and one pending order |
| `binh@example.com` | seller of "Gốm Bát Tràng Cô Bình" |
| `chi@example.com` | seller of "Áo Thun Sài Gòn" |

The mock data is saved in `localStorage` under `marketplace-mock-db:v2`. Delete that key to reset it.

Payments: online orders (VNPAY, STRIPE) start in `AWAITING_PAYMENT` with a 15-minute deadline. The
mock keeps the payment state machine in its DB, persisted to `localStorage` with the rest of the state; `window.location.assign('/__mock/pay/<id>')` opens
a fake gateway page ("Thành công" / "Thất bại") that settles the payment and returns to
`/checkout/result?payment_id=<id>`.

Ledger: wallet balances are derived from entries, never stored. When an online order completes, the seller
is credited total − commission (5 %). Payouts settle 3 s after the request and always succeed.
`binh@example.com` is seeded with an available balance of 457,500 ₫.

## API types

The OpenAPI files in `../api/openapi/` are the source of truth. `src/api/gen/*` is generated from
them and committed, because the Docker build context contains only `web/`. After a contract change,
run `npm run gen:api`; `npm run lint` fails until you do. `src/api/types.ts` only re-exports aliases
of the generated types.

## Auth

The access token is kept in memory only. On load the app calls `POST /api/auth/refresh`, which
uses the HttpOnly `refresh_token` cookie (`Path=/api/auth`), to restore the session. On a 401 the
API client refreshes once, shared by all concurrent requests, and retries the request once. If the
refresh fails, the user goes to `/login?next=…`.

## Docker

`Dockerfile` builds the SPA and serves it with nginx on port 80, proxying `/api/` to
`http://gateway:8080`. `deploy/docker-compose.web.yml` uses it (host port 3000).

## Layout

```
src/api          generated types, typed client, session/refresh, query hooks per resource
src/mocks        MSW handlers + in-memory database and seed data
src/features     auth, catalog, cart, checkout, orders, seller-products, seller-orders, shop
src/components   shared UI (layout, price, status tabs, quantity input, shadcn ui/)
src/routes       route table and guards
e2e              Playwright specs (MSW mode, plus a session check against a stub gateway)
```

## Deviations and decisions (WS5')

1. A wallet entry's order link targets `/orders/{id}`, because `WalletEntry` has no shop slug.
2. The first online submit reuses the idempotency key stored under `pay:{checkout_id}`; pay-again
   always renews it.
3. If payment creation fails after checkout, the user always lands on the AWAITING orders, with the
   deadline toast for 503/network errors and the reason message otherwise.
4. `meta.silent` mutations skip the global retry toast.
5. Mock payouts never fail.
6. The mock payment page (`/__mock/pay/:id`) is DEV-only.
7. `data-testid`s for WS6': `payment-method-COD|VNPAY|STRIPE`, `payment-result` (`data-status`),
   `pay-again`, `order-payment-status`, `payment-countdown`, `order-pay-button`, `wallet-available`,
   `wallet-in-escrow`, `wallet-pending-payout`, `wallet-entry`, `payout-amount`, `payout-submit`,
   `payout-row` (`data-status`).
