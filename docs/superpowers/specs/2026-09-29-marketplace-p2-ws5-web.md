# WS5': web changes for P2

> Depends on WS0' (`order.yaml`, `payment.yaml`, `ledger.yaml`) and on the P1.5 contract fixes `CartShop.shipping_fee` and `Order.shop.slug` (overview §7). Owns `web/`. During development all calls are served by MSW handlers typed from the OpenAPI specs, including a mock FAKE hosted page. This spec amends P1 `2026-09-28-marketplace-p1-ws5-web.md`. Stack, conventions and structure are unchanged. UI copy is Vietnamese; code is English.

## 1. Buyer changes

| Route | Change |
|---|---|
| `/checkout` | Payment method radio group: "Thanh toán khi nhận hàng" (`COD`), "VNPay" (`VNPAY`), "Thẻ quốc tế (Stripe)" (`STRIPE`). The order preview uses `CartShop.shipping_fee` from the server. The P1.5 fix removes the `SHIPPING_FEE_FLAT` constant, and this spec relies on that. For online methods it shows "Tổng thanh toán" = Σ totals of the selected shops. |
| `/checkout` submit | COD: as in P1. Online: see "Online submit" below. |
| `/checkout/result?payment_id=` | **New.** Polls `GET /api/payments/{id}` every 2 s (TanStack Query `refetchInterval`) until the status is terminal, or for at most 60 s. See the states below. |
| `/orders`, `/orders/:id` | New status tab and badge "Chờ thanh toán" (`AWAITING_PAYMENT`); `payment_status` badge (Chưa thanh toán / Đã thanh toán / Đang hoàn tiền / Đã hoàn tiền); a countdown to `payment_deadline`; a "Thanh toán" button on `AWAITING_PAYMENT` orders, which starts a new attempt for the checkout (see "Pay again" below); cancel stays available with its P1 dialog. |

**Online submit:**
1. `POST /api/checkout` with the P1 idempotency behaviour. The key is generated on mount and reused on retry.
2. On 201, `POST /api/payments {checkout_id}` with a **new** `Idempotency-Key`, stored in `sessionStorage` under `pay:{checkout_id}`. A double click or a reload reuses the same key.
3. `window.location.assign(payment.redirect_url)`.

If step 2 fails (503 or a network error), the page moves to `/orders?status=AWAITING_PAYMENT` with a toast: "Đơn đã được tạo, vui lòng thanh toán trước <deadline>". The orders exist and can be paid from there.

**Result page states:**
- `SUCCEEDED`: "Thanh toán thành công". Links to the orders, and invalidates the orders queries.
- `FAILED` / `EXPIRED`: "Thanh toán không thành công", with a "Thanh toán lại" button. The button calls `POST /api/payments` with a fresh key and redirects. It is hidden when the deadline has passed.
- `DUPLICATE`: "Giao dịch trùng — sẽ được hoàn tiền tự động".
- Still `PENDING` after 60 s: "Đang xác nhận thanh toán…", with a link to the orders. This is normal locally with the real VNPay, where the reconciler finishes the job.

**Pay again from an order** reuses the same call. A 409 with `details.reason = ALREADY_PAID` refreshes the order instead of showing an error.

## 2. Seller changes

| Route | Content |
|---|---|
| `/seller/wallet` | **New.** Three cards: Số dư khả dụng (`available`), Đang giữ (`in_escrow`), Đang rút (`pending_payout`). Below them, an entries table (time, kind label, order link through `Order.shop.slug` where relevant, a signed amount in green or red) with "load more" through the cursor. |
| `/seller/wallet` payout form | Amount input, validated against `PAYOUT_MIN_AMOUNT` (50,000, from a constant mirroring the contract description) and `available`. Submits `POST /api/seller/payouts` with an `Idempotency-Key` generated when the form opens. `INSUFFICIENT_FUNDS` shows `details.available`. |
| `/seller/payouts` | **New.** A table of payouts with status tabs (REQUESTED, SETTLED, FAILED), polling every 10 s while any payout is `REQUESTED`. |
| `/seller/orders` | Shows `AWAITING_PAYMENT` orders read-only ("Chờ người mua thanh toán"), with no actions. The order detail shows the commission and "Bạn nhận được" = total − commission for online orders. |
| Seller navigation | Adds "Ví" and "Rút tiền". |

## 3. Cross-cutting

- **Error mapping** adds `INSUFFICIENT_FUNDS`, plus `INVALID_STATE_TRANSITION` for the payment reasons `ALREADY_PAID`, `BELOW_PROVIDER_MINIMUM` and deadline passed. Each gets a Vietnamese message.
- **Money:** amounts stay integer VND and are formatted with the P1 `Intl.NumberFormat`. No arithmetic other than summing server-provided totals for display.
- **Redirects:** the app only ever navigates to a `redirect_url` returned by `POST /api/payments`. It never builds provider URLs itself.
- **MSW:**
  - the handlers implement the payment state machine in memory;
  - `redirect_url` points to a mock page at `/__mock/pay/:id` with "Thành công" / "Thất bại" buttons, which then lands on `/checkout/result`;
  - the wallet and payouts handlers keep in-memory balances consistent with the ledger rules (release credits `total − commission`; a payout debits `available`).
- **Accessibility:** the payment method radios are a labelled `fieldset`; the countdown uses `aria-live="polite"` and updates at most once per minute; the result page has a heading that announces the state.

## 4. Testing

- **Unit and component:**
  - payment method selection, and the preview total with the server `shipping_fee`;
  - the result page for each status (with fake timers for polling and its timeout);
  - pay-again with a fresh idempotency key, and reuse of the key from `sessionStorage` on reload;
  - wallet cards and the signed amounts in entries;
  - payout form validation and `INSUFFICIENT_FUNDS`;
  - error mapping.
- **Playwright (MSW mode):**
  - Online checkout from 2 shops → mock pay → result page shows success → both orders "Đã thanh toán".
  - Mock fail → "Thanh toán lại" → success.
  - Cancel an `AWAITING_PAYMENT` order → both orders of the checkout are cancelled.
  - Seller wallet shows a balance → payout request → appears as REQUESTED, then SETTLED.
- `npm run lint` is clean. Types are regenerated with `npm run gen:api`, and no request or response types are written by hand. The P1 `ProductInput` workaround may remain until its own contract fix lands.

## 5. Acceptance criteria

- [ ] Every flow in §1 and §2 works end to end in MSW mode.
- [ ] Against the real stack with the FAKE provider, WS6' can drive the online checkout, pay again, the wallet and the payout through the UI with stable `data-testid`s. The P1 selectors are unchanged.
- [ ] Lint, unit and Playwright suites are green.
