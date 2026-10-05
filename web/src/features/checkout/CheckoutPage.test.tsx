import { screen, within } from '@testing-library/react';
import { HttpResponse, delay, http } from 'msw';
import { expect, test, vi } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { server } from '@/test/server';
import { currentPath, renderApp } from '@/test/render';
import { redirectTo } from '@/lib/redirect';

vi.mock('@/lib/redirect', () => ({ redirectTo: vi.fn() }));

type User = ReturnType<typeof renderApp>['user'];

async function fillAddress(user: User) {
  await user.type(await screen.findByLabelText('Số điện thoại'), '0901234567');
  await user.type(screen.getByLabelText('Số nhà, tên đường'), '12 Lý Tự Trọng');
  await user.type(screen.getByLabelText('Phường/Xã'), 'Bến Nghé');
  await user.type(screen.getByLabelText('Quận/Huyện'), 'Quận 1');
  await user.type(screen.getByLabelText('Tỉnh/Thành phố'), 'TP. Hồ Chí Minh');
}

function recordCheckoutKeys(onRequest?: (n: number) => Promise<Response | undefined> | Response | undefined) {
  const keys: string[] = [];
  server.use(
    http.post('*/api/checkout', async ({ request }) => {
      keys.push(request.headers.get('Idempotency-Key') ?? '');
      return onRequest?.(keys.length); // undefined → fall through to the default mock handler
    }),
  );
  return keys;
}

const twoShopUrl = `/checkout?sku=${SEED.skus.bowl11}&sku=${SEED.skus.teeM}`;

test('previews one order per shop with the flat shipping fee and places them', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 };
  const before = getDb().state.orders.length;
  const { user } = renderApp(twoShopUrl, { as: 'an@example.com' });

  const bowlShop = await screen.findByRole('region', { name: 'Đơn hàng từ Gốm Bát Tràng Cô Bình' });
  expect(within(bowlShop).getAllByText('90.000 ₫')).toHaveLength(2); // the only line + the shop subtotal
  expect(within(bowlShop).getByText('30.000 ₫')).toBeInTheDocument();
  expect(screen.getByText('Mỗi shop tạo một đơn hàng riêng (2 đơn).')).toBeInTheDocument();
  expect(screen.getByText('299.000 ₫')).toBeInTheDocument();
  expect(screen.getByLabelText('Họ và tên người nhận')).toHaveValue('An Nguyễn');

  await fillAddress(user);
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));

  await screen.findByRole('heading', { level: 1, name: 'Đơn mua' });
  expect(currentPath()).toBe('/orders?placed=2');
  expect(getDb().state.orders.length).toBe(before + 2);
});

test('previews the shipping fee the cart reports for each shop', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 };
  server.use(
    http.get('*/api/cart', () => {
      const cart = getDb().cart(SEED.users.an);
      return HttpResponse.json({ ...cart, shops: cart.shops.map((s) => ({ ...s, shipping_fee: 45_000 })) });
    }),
  );
  renderApp(twoShopUrl, { as: 'an@example.com' });

  const bowlShop = await screen.findByRole('region', { name: 'Đơn hàng từ Gốm Bát Tràng Cô Bình' });
  expect(within(bowlShop).getByText('45.000 ₫')).toBeInTheDocument();
  expect(screen.getByText('329.000 ₫')).toBeInTheDocument(); // 90.000 + 149.000 + 2 × 45.000
});

test('client validation blocks an invalid phone number', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  const keys = recordCheckoutKeys();
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.clear(screen.getByLabelText('Số điện thoại'));
  await user.type(screen.getByLabelText('Số điện thoại'), '12345');
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  expect(await screen.findByText('Số điện thoại không hợp lệ')).toBeInTheDocument();
  expect(keys).toEqual([]);
});

test('a double-click sends a single checkout request', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  const keys = recordCheckoutKeys(async () => {
    await delay(50);
    return undefined;
  });
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.dblClick(screen.getByRole('button', { name: 'Đặt hàng' }));
  await screen.findByRole('heading', { level: 1, name: 'Đơn mua' });
  expect(currentPath()).toBe('/orders?placed=1');
  expect(keys).toHaveLength(1);
});

test('retrying after a 503 reuses the same Idempotency-Key', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  const keys = recordCheckoutKeys((n) =>
    n === 1 ? HttpResponse.json({ error: { code: 'UPSTREAM_UNAVAILABLE', message: 'down' } }, { status: 503 }) : undefined,
  );
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  expect(await screen.findByText('Dịch vụ đang tạm gián đoạn. Vui lòng thử lại.')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  await screen.findByRole('heading', { level: 1, name: 'Đơn mua' });
  expect(currentPath()).toBe('/orders?placed=1');
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
});

test('a lost checkout response keeps the form so the buyer can retry with the same key', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  const keys = recordCheckoutKeys((n) => {
    if (n !== 1) return undefined;
    // The server committed the checkout and emptied the cart, but the response never arrived.
    getDb().state.carts[SEED.users.an] = {};
    return HttpResponse.error();
  });
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  expect((await screen.findAllByText('Dịch vụ đang tạm gián đoạn. Vui lòng thử lại.')).length).toBeGreaterThan(0);
  await new Promise((resolve) => setTimeout(resolve, 100)); // let a (wrong) cart refetch land
  expect(screen.queryByText('Không có sản phẩm nào để thanh toán')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Đặt hàng' })).toBeEnabled();
  expect(keys).toHaveLength(1);
});

test('each checkout page visit gets a fresh key', async () => {
  const keys = recordCheckoutKeys();
  for (let i = 0; i < 2; i += 1) {
    getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
    const { user, unmount } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
    await fillAddress(user);
    await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
    await screen.findByRole('heading', { level: 1, name: 'Đơn mua' });
    unmount();
  }
  expect(keys).toHaveLength(2);
  expect(keys[0]).not.toBe(keys[1]);
});

test('OUT_OF_STOCK sends the buyer back to the cart with the line flagged', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.vase]: 2 };
  const { user } = renderApp(`/checkout?sku=${SEED.skus.vase}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  expect(await screen.findByText('Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.')).toBeInTheDocument();
  expect(currentPath()).toBe('/cart');
  expect(screen.getByText('Chỉ còn 1 sản phẩm')).toBeInTheDocument();
});

test('server VALIDATION_ERROR details land on the address fields', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  recordCheckoutKeys(() =>
    HttpResponse.json({ error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { 'shipping_address.ward': 'unknown ward' } } } }, { status: 400 }),
  );
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  await user.click(screen.getByRole('button', { name: 'Đặt hàng' }));
  expect(await screen.findByText('Giá trị không hợp lệ')).toBeInTheDocument();
  expect(screen.getByLabelText('Phường/Xã')).toHaveAttribute('aria-invalid', 'true');
});

test('nothing selected shows a way back to the cart', async () => {
  renderApp('/checkout', { as: 'an@example.com' });
  expect(await screen.findByText('Không có sản phẩm nào để thanh toán')).toBeInTheDocument();
});

test('payment method radios default to COD', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });

  const fieldset = await screen.findByRole('group', { name: 'Phương thức thanh toán' });
  const radios = within(fieldset).getAllByRole('radio');
  expect(radios).toHaveLength(3);

  const cod = within(fieldset).getByDisplayValue('COD');
  const vnpay = within(fieldset).getByDisplayValue('VNPAY');
  const stripe = within(fieldset).getByDisplayValue('STRIPE');

  expect(cod).toBeChecked();
  expect(vnpay).not.toBeChecked();
  expect(stripe).not.toBeChecked();

  expect(within(fieldset).getByTestId('payment-method-COD')).toBeInTheDocument();
  expect(within(fieldset).getByTestId('payment-method-VNPAY')).toBeInTheDocument();
  expect(within(fieldset).getByTestId('payment-method-STRIPE')).toBeInTheDocument();
});

test('choosing VNPAY and submitting calls startPayment with correct idempotency key', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  let checkoutPaymentMethod: string | undefined;
  let paymentIdempotencyKey: string | undefined;
  
  server.use(
    http.post('*/api/checkout', async ({ request }) => {
      const idemKey = request.headers.get('Idempotency-Key') ?? '';
      const body = await request.json() as { payment_method?: string };
      checkoutPaymentMethod = body.payment_method;
      const response = getDb().checkout(SEED.users.an, idemKey, body as any);
      return HttpResponse.json(response, { status: 201 });
    }),
    http.post('*/api/payments', async ({ request }) => {
      paymentIdempotencyKey = request.headers.get('Idempotency-Key') ?? undefined;
      const body = await request.json() as { checkout_id: string };
      const response = getDb().createPayment(SEED.users.an, paymentIdempotencyKey ?? '', body);
      return HttpResponse.json(response, { status: 201 });
    }),
  );
  
  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);
  
  const fieldset = await screen.findByRole('group', { name: 'Phương thức thanh toán' });
  const vnpayRadio = within(fieldset).getByDisplayValue('VNPAY');
  await user.click(vnpayRadio);
  
  expect(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' })).toBeInTheDocument();
  
  await user.click(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' }));

  expect(checkoutPaymentMethod).toBe('VNPAY');
  expect(paymentIdempotencyKey).toBeDefined();
  expect(typeof paymentIdempotencyKey).toBe('string');
  expect(redirectTo).toHaveBeenCalledWith(expect.stringMatching(/\/__mock\/pay\//));
});

test('payment method selection persists and sends correct payment method', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
  let paymentMethods: (string | undefined)[] = [];

  server.use(
    http.post('*/api/checkout', async ({ request }) => {
      const body = await request.json() as { payment_method?: string };
      paymentMethods.push(body.payment_method);
      return undefined;
    }),
  );

  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);

  const fieldset = await screen.findByRole('group', { name: 'Phương thức thanh toán' });
  const stripeRadio = within(fieldset).getByDisplayValue('STRIPE');
  await user.click(stripeRadio);

  expect(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' })).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' }));

  expect(paymentMethods).toContain('STRIPE');
});

test('payment 503 error shows retryable toast and navigates to AWAITING_PAYMENT', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };

  server.use(
    http.post('*/api/payments', () => HttpResponse.json({ error: { code: 'UPSTREAM_UNAVAILABLE', message: 'service down' } }, { status: 503 })),
  );

  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);

  const fieldset = await screen.findByRole('group', { name: 'Phương thức thanh toán' });
  const vnpayRadio = within(fieldset).getByDisplayValue('VNPAY');
  await user.click(vnpayRadio);

  await user.click(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' }));

  expect(await screen.findByText(/Đơn đã được tạo, vui lòng thanh toán trước/)).toBeInTheDocument();
  expect(currentPath()).toBe('/orders?status=AWAITING_PAYMENT');
});

test('payment 409 BELOW_PROVIDER_MINIMUM shows reason and navigates', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };

  server.use(
    http.post('*/api/payments', () =>
      HttpResponse.json(
        { error: { code: 'INVALID_STATE_TRANSITION', message: 'below minimum', details: { reason: 'BELOW_PROVIDER_MINIMUM' } } },
        { status: 409 },
      ),
    ),
  );

  const { user } = renderApp(`/checkout?sku=${SEED.skus.bowl11}`, { as: 'an@example.com' });
  await fillAddress(user);

  const fieldset = await screen.findByRole('group', { name: 'Phương thức thanh toán' });
  const vnpayRadio = within(fieldset).getByDisplayValue('VNPAY');
  await user.click(vnpayRadio);

  await user.click(screen.getByRole('button', { name: 'Đặt hàng và thanh toán' }));

  expect(await screen.findByText('Số tiền thấp hơn mức tối thiểu của cổng thanh toán. Vui lòng chọn phương thức khác.')).toBeInTheDocument();
  expect(currentPath()).toBe('/orders?status=AWAITING_PAYMENT');
});
