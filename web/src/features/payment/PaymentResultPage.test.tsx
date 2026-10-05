import { screen, waitFor } from '@testing-library/react';
import { HttpResponse, delay, http } from 'msw';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { server } from '@/test/server';
import { currentPath, renderApp } from '@/test/render';
import { redirectTo } from '@/lib/redirect';
import { toast } from 'sonner';

vi.mock('@/lib/redirect', () => ({ redirectTo: vi.fn() }));
vi.mock('sonner', async () => {
  const actual = await vi.importActual('sonner');
  return {
    ...actual,
    toast: {
      success: vi.fn(),
      error: vi.fn(),
    },
  };
});

beforeEach(() => {
  vi.mocked(redirectTo).mockClear();
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  vi.useRealTimers();
});

function setupCart() {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 1 };
}

function setupCheckout() {
  setupCart();
  const checkoutResponse = getDb().checkout(SEED.users.an, 'k', {
    shipping_address: {
      full_name: 'An Nguyễn',
      phone: '0901234567',
      line1: '12 Lý Tự Trọng',
      ward: 'Bến Nghé',
      district: 'Quận 1',
      province: 'TP. Hồ Chí Minh',
    },
    payment_method: 'VNPAY',
  });
  return checkoutResponse.checkout_id;
}

test('pending then success: polls and updates heading when status changes', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  // Should show PENDING heading
  const heading = await screen.findByRole('heading', { name: 'Đang chờ kết quả thanh toán…' });
  expect(heading).toBeInTheDocument();

  // Verify the section has correct data-testid and data-status
  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'PENDING');

  // Simulate payment success in the mock db
  getDb().submitMockPayment(payment.id, 'succeed');

  // After polling picks it up (within 2s + some buffer), heading should change
  const successHeading = await screen.findByRole('heading', { name: 'Thanh toán thành công' }, { timeout: 4000 });
  expect(successHeading).toBeInTheDocument();
});

test('failed state shows heading and pay-again button', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  // Set to FAILED before rendering
  getDb().submitMockPayment(payment.id, 'fail');

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Thanh toán không thành công' });
  expect(heading).toBeInTheDocument();

  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'FAILED');

  // Should show pay-again button
  expect(screen.getByTestId('pay-again')).toBeInTheDocument();

  // Should show order link
  expect(screen.getByRole('link', { name: 'Xem đơn mua' })).toHaveAttribute('href', '/orders');
});

test('pay-again button sends new POST and redirects', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  getDb().submitMockPayment(payment.id, 'fail');

  const newIdempotencyKeys: string[] = [];
  server.use(
    http.post('*/api/payments', async ({ request }) => {
      const key = request.headers.get('Idempotency-Key') ?? '';
      newIdempotencyKeys.push(key);
      // Let the default handler process it to capture the new payment ID
      return undefined;
    }),
  );

  const { user } = renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const payAgainBtn = await screen.findByTestId('pay-again');
  await user.click(payAgainBtn);

  // Should have called startPayment which calls POST /api/payments
  await waitFor(() => {
    expect(newIdempotencyKeys.length).toBeGreaterThan(0);
  });

  // The idempotency key should be different from 'p1'
  expect(newIdempotencyKeys[0]).not.toBe('p1');

  // Should redirect to the mock payment page
  await waitFor(() => {
    expect(vi.mocked(redirectTo)).toHaveBeenCalled();
    const redirectUrl = vi.mocked(redirectTo).mock.calls[0]?.[0] ?? '';
    expect(redirectUrl).toMatch(/\/__mock\/pay\//);
  });
});

test('pay-again with 409 ALREADY_PAID shows toast and does not redirect', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  getDb().submitMockPayment(payment.id, 'fail');

  server.use(
    http.post('*/api/payments', () => {
      return HttpResponse.json(
        {
          error: {
            code: 'INVALID_STATE_TRANSITION',
            message: 'payment already succeeded',
            details: { reason: 'ALREADY_PAID' },
          },
        },
        { status: 409 },
      );
    }),
  );

  const { user } = renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const payAgainBtn = await screen.findByTestId('pay-again');
  await user.click(payAgainBtn);

  // Should show success toast with the Vietnamese message
  await waitFor(() => {
    expect(vi.mocked(toast.success)).toHaveBeenCalledWith('Đơn hàng đã được thanh toán.');
  });

  // Should not redirect
  expect(vi.mocked(redirectTo)).not.toHaveBeenCalled();
});

test('failed state with expired expires_at does not show pay-again button', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  getDb().submitMockPayment(payment.id, 'fail');

  // Set expires_at to the past
  const pastDate = new Date(Date.now() - 60_000).toISOString();
  getDb().state.payments[0]!.expires_at = pastDate;

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Thanh toán không thành công' });
  expect(heading).toBeInTheDocument();

  // Button should not be present
  expect(screen.queryByTestId('pay-again')).not.toBeInTheDocument();

  // But link to orders should still be there
  expect(screen.getByRole('link', { name: 'Xem đơn mua' })).toBeInTheDocument();
});

test('expired state shows heading', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  // Set to EXPIRED
  getDb().state.payments[0]!.status = 'EXPIRED';

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Thanh toán không thành công' });
  expect(heading).toBeInTheDocument();

  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'EXPIRED');
});

test('duplicate state shows heading', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  // Set to DUPLICATE
  getDb().state.payments[0]!.status = 'DUPLICATE';

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Giao dịch trùng — sẽ được hoàn tiền tự động' });
  expect(heading).toBeInTheDocument();

  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'DUPLICATE');
});

test('timeout: after 60s, heading changes and polling stops', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });

  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  let getCount = 0;
  server.use(
    http.get('*/api/payments/*', () => {
      getCount++;
      return undefined; // Use default handler
    }),
  );

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  // Initially shows PENDING
  const heading = await screen.findByRole('heading', { name: 'Đang chờ kết quả thanh toán…' });
  expect(heading).toBeInTheDocument();

  // Let some polling happen (2s interval, so should have at least 2 requests)
  await vi.advanceTimersByTimeAsync(5_000);
  const pollCount = getCount;
  expect(pollCount).toBeGreaterThan(1); // Proves polling was running

  // Advance time by 60 seconds total
  await vi.advanceTimersByTimeAsync(55_000);

  // Heading should change to the timed-out version
  const timedOutHeading = await screen.findByRole('heading', { name: 'Đang xác nhận thanh toán…' });
  expect(timedOutHeading).toBeInTheDocument();

  // data-status should still be PENDING but in the timed-out state
  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'PENDING');

  // The hint text about the system should be visible
  expect(screen.getByText('Hệ thống sẽ cập nhật đơn hàng khi có kết quả.')).toBeInTheDocument();

  const initialGetCount = getCount;

  // Advance further - polling should have stopped
  await vi.advanceTimersByTimeAsync(10_000);

  // GET count should not increase (polling stopped)
  expect(getCount).toBe(initialGetCount);
});

test('no payment_id param shows empty state', async () => {
  setupCheckout();
  renderApp('/checkout/result', { as: 'an@example.com' });

  const title = await screen.findByText('Không tìm thấy giao dịch');
  expect(title).toBeInTheDocument();

  // Should have link to orders
  expect(screen.getByRole('link', { name: 'Xem đơn mua' })).toHaveAttribute('href', '/orders');
});

test('unknown payment id shows empty state', async () => {
  setupCheckout();
  const unknownId = '00000000-0000-0000-0000-000000000000';

  renderApp(`/checkout/result?payment_id=${unknownId}`, { as: 'an@example.com' });

  const title = await screen.findByText('Không tìm thấy giao dịch');
  expect(title).toBeInTheDocument();
});

test('succeeded state shows heading and link to orders', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  // Set to SUCCEEDED
  getDb().state.payments[0]!.status = 'SUCCEEDED';

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Thanh toán thành công' });
  expect(heading).toBeInTheDocument();

  const section = screen.getByTestId('payment-result');
  expect(section).toHaveAttribute('data-status', 'SUCCEEDED');

  // Should have link to orders
  expect(screen.getByRole('link', { name: 'Xem đơn mua' })).toHaveAttribute('href', '/orders');
});

test('MockPayPage: renders form and clicking buttons submits to mock endpoint', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  const { user } = renderApp(`/__mock/pay/${payment.id}`, { as: 'an@example.com' });

  const heading = await screen.findByRole('heading', { name: 'Cổng thanh toán giả lập' });
  expect(heading).toBeInTheDocument();

  // Should show payment id
  expect(screen.getByText(payment.id)).toBeInTheDocument();

  // Should show the mock-only hint
  expect(screen.getByText('Trang này chỉ có ở chế độ mock.')).toBeInTheDocument();

  // Click "Thất bại" button
  const failBtn = screen.getByRole('button', { name: 'Thất bại' });
  await user.click(failBtn);

  // Should navigate to result page
  await waitFor(() => {
    expect(currentPath()).toBe(`/checkout/result?payment_id=${payment.id}`);
  });

  // Mock db should show payment as FAILED
  expect(getDb().payment(SEED.users.an, payment.id).status).toBe('FAILED');
});

test('MockPayPage: clicking success button', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  const { user } = renderApp(`/__mock/pay/${payment.id}`, { as: 'an@example.com' });

  // Wait for the mock page to load
  await screen.findByRole('heading', { name: 'Cổng thanh toán giả lập' });

  // Click "Thành công" button
  const successBtn = screen.getByRole('button', { name: 'Thành công' });
  await user.click(successBtn);

  // Should navigate to result page
  await waitFor(() => {
    expect(currentPath()).toBe(`/checkout/result?payment_id=${payment.id}`);
  });

  // Mock db should show payment as SUCCEEDED
  expect(getDb().payment(SEED.users.an, payment.id).status).toBe('SUCCEEDED');
});

test('503 error shows ErrorState with retry button', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  server.use(
    http.get('*/api/payments/*', () => {
      return HttpResponse.json({}, { status: 503 });
    }),
  );

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  // Should show ErrorState with "Dịch vụ đang tạm gián đoạn"
  const errorMsg = await screen.findByText('Dịch vụ đang tạm gián đoạn. Vui lòng thử lại.');
  expect(errorMsg).toBeInTheDocument();

  // Should have a retry button
  const retryBtn = screen.getByRole('button', { name: 'Thử lại' });
  expect(retryBtn).toBeInTheDocument();
});

test('SUCCEEDED status invalidates orders and order queries', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });

  const { queryClient } = renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  // Wait for PENDING heading
  await screen.findByRole('heading', { name: 'Đang chờ kết quả thanh toán…' });

  // Create a spy on invalidateQueries BEFORE the effect runs
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

  // Update payment to SUCCEEDED (this should trigger the polling to pick it up within 2s)
  getDb().submitMockPayment(payment.id, 'succeed');

  // Wait for the heading to change to SUCCEEDED (polling picks it up)
  await screen.findByRole('heading', { name: 'Thanh toán thành công' }, { timeout: 4000 });

  // Now check that invalidateQueries was called with the correct keys
  await waitFor(() => {
    const calls = invalidateSpy.mock.calls;
    const hasOrdersCall = calls.some((call) => JSON.stringify(call[0]) === JSON.stringify({ queryKey: ['orders'] }));
    const hasOrderCall = calls.some((call) => JSON.stringify(call[0]) === JSON.stringify({ queryKey: ['order'] }));
    expect(hasOrdersCall).toBe(true);
    expect(hasOrderCall).toBe(true);
  });

  invalidateSpy.mockRestore();
});

test('pay-again is disabled while starting, so a double click sends one POST', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  getDb().submitMockPayment(payment.id, 'fail');
  let posts = 0;
  server.use(
    http.post('*/api/payments', async () => {
      posts++;
      await delay(100);
      return undefined;
    }),
  );

  const { user } = renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });
  const btn = await screen.findByTestId('pay-again');
  await user.dblClick(btn);

  await waitFor(() => expect(vi.mocked(redirectTo)).toHaveBeenCalled());
  expect(posts).toBe(1);
});

test('the result section announces state changes politely', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });

  expect(await screen.findByTestId('payment-result')).toHaveAttribute('aria-live', 'polite');
  expect(screen.getByRole('status')).toHaveTextContent('Đang chờ kết quả thanh toán…');
});

test('a failed poll keeps rendering the last known payment', async () => {
  const checkoutId = setupCheckout();
  const payment = getDb().createPayment(SEED.users.an, 'p1', { checkout_id: checkoutId });
  let gets = 0;
  server.use(
    http.get('*/api/payments/*', () => {
      gets++;
      return gets === 1 ? undefined : HttpResponse.json({}, { status: 503 });
    }),
  );

  renderApp(`/checkout/result?payment_id=${payment.id}`, { as: 'an@example.com' });
  await screen.findByRole('heading', { name: 'Đang chờ kết quả thanh toán…' });
  await waitFor(() => expect(gets).toBeGreaterThan(1), { timeout: 4000 });

  expect(screen.getByRole('heading', { name: 'Đang chờ kết quả thanh toán…' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Thử lại' })).not.toBeInTheDocument();
});
