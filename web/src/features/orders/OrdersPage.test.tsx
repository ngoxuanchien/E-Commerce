import { screen, within } from '@testing-library/react';
import { expect, test, vi, beforeEach } from 'vitest';
import { waitFor } from '@testing-library/react';
import { http } from 'msw';
import { server } from '@/test/server';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';

vi.mock('@/lib/redirect', () => ({ redirectTo: vi.fn() }));

beforeEach(() => {
  vi.mocked(redirectTo).mockClear();
});

const { redirectTo } = await import('@/lib/redirect');

test('lists the buyer orders newest first and filters by status tab', async () => {
  const { user } = renderApp('/orders', { as: 'an@example.com' });
  await screen.findByRole('article', { name: 'Đơn hàng của Áo Thun Sài Gòn' });
  expect(screen.getAllByRole('article').map((c) => c.getAttribute('aria-label'))).toEqual([
    'Đơn hàng của Áo Thun Sài Gòn',
    'Đơn hàng của Gốm Bát Tràng Cô Bình',
  ]);
  await user.click(screen.getByRole('button', { name: 'Đã giao' }));
  expect(currentPath()).toBe('/orders?status=DELIVERED');
  expect(await screen.findByRole('button', { name: 'Đã giao', pressed: true })).toBeInTheDocument();
  await screen.findByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' });
  expect(screen.getAllByRole('article')).toHaveLength(1);
});

test('confirms a just-placed checkout', async () => {
  renderApp('/orders?placed=2', { as: 'an@example.com' });
  expect(await screen.findByText('Đặt hàng thành công. Đã tạo 2 đơn hàng, mỗi shop một đơn.')).toBeInTheDocument();
});

test('an empty tab invites the user to keep shopping', async () => {
  renderApp('/orders?status=SHIPPING', { as: 'an@example.com' });
  expect(await screen.findByText('Chưa có đơn hàng nào ở trạng thái này.')).toBeInTheDocument();
});

test('shows awaiting payment orders with payment status and countdown', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
  db.checkout(SEED.users.an, 'test-checkout', {
    sku_ids: [SEED.skus.bowl11, SEED.skus.teeM],
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
  renderApp('/orders?status=AWAITING_PAYMENT', { as: 'an@example.com' });
  const cards = await screen.findAllByRole('article');
  expect(cards).toHaveLength(2);
  for (const card of cards) {
    expect(within(card).getByTestId('order-payment-status')).toHaveTextContent('Chưa thanh toán');
    expect(within(card).getByTestId('payment-countdown')).toHaveTextContent('Còn 14 phút để thanh toán');
  }
});

test('clicking pay button sends payment request with different idempotency keys', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
  const checkout = db.checkout(SEED.users.an, 'test-checkout-2', {
    sku_ids: [SEED.skus.bowl11, SEED.skus.teeM],
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

  const requests: Array<{ key: string | null; body: { checkout_id: string } }> = [];
  server.use(
    http.post('*/api/payments', async ({ request }) => {
      requests.push({
        key: request.headers.get('Idempotency-Key'),
        body: (await request.clone().json()) as { checkout_id: string },
      });
      return undefined;
    }),
  );

  const { user } = renderApp('/orders?status=AWAITING_PAYMENT', { as: 'an@example.com' });
  const buttons = await screen.findAllByTestId('order-pay-button');
  expect(buttons).toHaveLength(2);

  await user.click(buttons[0]);
  await waitFor(() => expect(vi.mocked(redirectTo)).toHaveBeenCalledTimes(1));

  await user.click(buttons[1]);
  await waitFor(() => expect(vi.mocked(redirectTo)).toHaveBeenCalledTimes(2));

  expect(requests).toHaveLength(2);
  expect(requests[0].body.checkout_id).toBe(checkout.checkout_id);
  expect(requests[1].body.checkout_id).toBe(checkout.checkout_id);
  expect(requests[0].key).not.toBeNull();
  expect(requests[1].key).not.toBeNull();
  expect(requests[0].key).not.toBe(requests[1].key);
});
