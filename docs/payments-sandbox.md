# Manual real-sandbox payment runbook

This guide is for testing the marketplace against real **Stripe test mode** and
**VNPay sandbox**. By default, `make up-app` uses the FAKE provider (no external
dependencies). To run through a real sandbox, follow one of the sections below.

## Prerequisites

- A running stack: `make up-app && make seed`
- Credentials for the sandbox you want to test
- `deploy/secrets/payment.env` created (see setup below)

## Stripe test mode

### Setup

1. Go to [Stripe Dashboard](https://dashboard.stripe.com/test/apikeys) and copy
   your **Secret key** (starts with `sk_test_`).

2. Create `deploy/secrets/payment.env` (or edit if it exists):

   ```bash
   cp deploy/secrets/payment.env.example deploy/secrets/payment.env
   ```

   Add your secret key:

   ```
   STRIPE_SECRET_KEY=sk_test_...
   PROVIDER_STRIPE=stripe
   ```

3. In another terminal, start the webhook forwarder:

   ```bash
   make stripe-listen
   ```

   This command prints a `whsec_...` value. Copy it into `deploy/secrets/payment.env`:

   ```
   STRIPE_WEBHOOK_SECRET=whsec_...
   ```

4. Restart the payment service:

   ```bash
   docker compose restart payment
   ```

   Wait for it to be ready (`docker compose logs payment` shows no errors).

### Testing

1. Open http://localhost:3000, log in as a buyer, add items from different shops, and check out.

2. At the payment method step, select **STRIPE** and continue.

3. You will see the Stripe-hosted checkout page. Use test card `4242 4242 4242 4242`
   (any future expiry date, any 3-digit CVC).

4. Complete the payment. Expect:
   - The checkout redirects to `/checkout/result?status=success`
   - The order appears in the seller center as "Awaiting Confirmation"
   - The seller's wallet shows the amount in "In Escrow"

5. Verify webhook delivery:
   - Check the payment service logs: `docker compose logs payment | grep -i stripe`
   - Look for `checkout.session.completed` and `charge` events

### Testing failures (optional)

- **Failed payment:** Use card `4000 0000 0000 0002`
- **Requires authentication:** Use card `4000 0025 0000 3155`

See [Stripe test card list](https://stripe.com/docs/testing) for more.

## VNPay sandbox

### Setup

1. Register a sandbox merchant account at
   [VNPay Sandbox Registration](https://sandbox.vnpayment.vn/devreg/)

2. After registration, note your:
   - `Mã Merchant Code` (TMN Code)
   - Hash secret (for signing requests)

3. Create or edit `deploy/secrets/payment.env`:

   ```bash
   cp deploy/secrets/payment.env.example deploy/secrets/payment.env
   ```

   Add your VNPay credentials:

   ```
   VNPAY_TMN_CODE=<your merchant code>
   VNPAY_HASH_SECRET=<your hash secret>
   VNPAY_PAY_URL=https://sandbox.vnpayment.vn/paymentv2/vpcpay.html
   VNPAY_API_URL=https://sandbox.vnpayment.vn/merchant_webapi/api/transaction
   PROVIDER_VNPAY=vnpay
   ```

4. Restart the payment service:

   ```bash
   docker compose restart payment
   ```

   Wait for it to be ready.

### Testing (without IPN tunnel)

The payment flow works without a public URL—the return URL redirect triggers
payment verification, and the reconciler handles dropped webhooks.

1. Open http://localhost:3000, log in as a buyer, add items, and check out.

2. Select **VNPAY** and continue.

3. You will be redirected to the VNPay sandbox page. Use a test card listed in
   [VNPay sandbox docs](https://sandbox.vnpayment.vn/apis/) (e.g., NCB test card).

4. Complete the payment. Expect:
   - Redirect back to `/checkout/result?status=success`
   - The order appears in the seller center
   - Wallet shows the amount in escrow

### Testing IPN (real-time notifications, optional)

To receive real-time IPN callbacks from VNPay, your server needs a public URL.

1. Start a tunnel to your local gateway:

   ```bash
   cloudflared tunnel --url http://localhost:8080
   ```

   This prints a public URL like `https://xyz.trycloudflare.com`.

2. In the VNPay merchant portal, register the IPN URL:

   ```
   https://xyz.trycloudflare.com/api/payments/vnpay/ipn
   ```

3. Run through a checkout again. VNPay will POST the IPN callback to your tunnel,
   and the payment service will log it.

### Testing refunds (optional)

After a successful payment, cancel the order from the seller center. The payment
service will request a refund from VNPay. Check the logs for the refund response.

## Switching providers

You can mix and match providers independently:

```
PROVIDER_VNPAY=fake  # Use FAKE for VNPay
PROVIDER_STRIPE=stripe  # Use real Stripe
```

Or both real:

```
PROVIDER_VNPAY=vnpay
PROVIDER_STRIPE=stripe
```

Just restart the payment service after changing `deploy/secrets/payment.env`.

## Manual verification checklist

Use this table to track successful sandbox tests. Fill in the date, which provider
you tested, and whether the key flows worked:

| Date | Provider | Checkout paid | Refund on cancel | Notes |
|---|---|---|---|---|
| | | | | |
| | | | | |
| | | | | |
| | | | | |
| | | | | |

**Checkout paid:** Order moved to "Awaiting Confirmation" and wallet shows escrow.

**Refund on cancel:** After cancelling, seller wallet escrow amount decreased (or
became zero). For Stripe, check the Stripe dashboard for a `charge.refunded` event.
For VNPay, check the payment service logs for the refund API response.

## Troubleshooting

- **Payment service fails to start:** Check `docker compose logs payment` for
  missing or invalid credentials. Make sure `PROVIDER_*` vars are `fake`, `vnpay`,
  or `stripe` (case-sensitive).

- **Webhook times out:** Stripe webhooks forward via `make stripe-listen`. If it
  hangs, restart it (Ctrl+C, then run again). VNPay webhooks require a tunnel if
  you want real IPN delivery (optional; the reconciler catches missed webhooks).

- **"BELOW_PROVIDER_MINIMUM" error:** The checkout total is smaller than Stripe's
  minimum (default 10,000 VND). Add more items or increase the product price in
  the seed data.

- **"CONFLICT" on create:** Two overlapping payment attempts for the same checkout.
  This is retried automatically by the client; if it persists, check if a previous
  attempt is still `PENDING` (ask the developer to query `SELECT * FROM payments WHERE
  checkout_id = '...' ORDER BY created_at DESC`).

## Reverting to FAKE

To go back to the FAKE provider (no external dependencies):

1. Edit or delete `deploy/secrets/payment.env`.
2. Restart: `docker compose restart payment` or `make up-app`.

The FAKE provider is always available and does not require any credentials or
external setup.
