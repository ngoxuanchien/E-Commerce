import { screen, within } from '@testing-library/react';
import { expect, test, vi, beforeEach } from 'vitest';
import { http, HttpResponse } from 'msw';
import { server } from '@/test/server';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { renderApp } from '@/test/render';

vi.mock('@/lib/redirect', () => ({ redirectTo: vi.fn() }));

beforeEach(() => {
  vi.mocked(redirectTo).mockClear();
});

const { redirectTo } = await import('@/lib/redirect');

test('shows items, address, totals and the chronological history', async () => {
  renderApp(`/orders/${SEED.orders.delivered}`, { as: 'an@example.com' });
  expect(await screen.findByRole('heading', { level: 1, name: /Đơn hàng/ })).toBeInTheDocument();
  const timeline = screen.getByRole('list', { name: 'Lịch sử đơn hàng' });
  expect(within(timeline).getAllByRole('listitem').map((li) => li.querySelector('p')?.textContent)).toEqual([
    'Đang xử lý', 'Chờ xác nhận', 'Đã xác nhận', 'Đang giao', 'Đã giao',
  ]);
  expect(screen.getByText('12 Lý Tự Trọng, Bến Nghé, Quận 1, TP. Hồ Chí Minh')).toBeInTheDocument();
  expect(screen.getByText('120.000 ₫')).toBeInTheDocument();
});

test('the buyer confirms receipt of a delivered order', async () => {
  const { user } = renderApp(`/orders/${SEED.orders.delivered}`, { as: 'an@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Đã nhận được hàng' }));
  expect(await screen.findByText('Hoàn thành', { selector: '[data-status]' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Đã nhận được hàng' })).not.toBeInTheDocument();
});

test('the buyer cancels a pending order with a reason and stock is returned', async () => {
  const stock = () => getDb().product(SEED.products.tee).skus.find((s) => s.id === SEED.skus.teeM)!.stock;
  const before = stock();
  const { user } = renderApp(`/orders/${SEED.orders.pending}`, { as: 'an@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Huỷ đơn hàng' }));
  const dialog = await screen.findByRole('dialog', { name: 'Huỷ đơn hàng' });
  await user.click(within(dialog).getByRole('button', { name: 'Xác nhận huỷ' }));
  expect(await within(dialog).findByText('Vui lòng nhập lý do huỷ')).toBeInTheDocument();
  await user.type(within(dialog).getByLabelText('Lý do huỷ'), 'Đặt nhầm size');
  await user.click(within(dialog).getByRole('button', { name: 'Xác nhận huỷ' }));
  expect(await screen.findByText('Lý do huỷ: Đặt nhầm size')).toBeInTheDocument();
  expect(stock()).toBe(before + 1);
});

test('the seller can view the order but gets no buyer actions', async () => {
  renderApp(`/orders/${SEED.orders.delivered}`, { as: 'binh@example.com' });
  expect(await screen.findByRole('heading', { level: 1, name: /Đơn hàng/ })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Đã nhận được hàng' })).not.toBeInTheDocument();
});

test("other users' orders are not found", async () => {
  renderApp(`/orders/${SEED.orders.delivered}`, { as: 'chi@example.com' });
  expect(await screen.findByText('Không tìm thấy đơn hàng')).toBeInTheDocument();
});

test('awaiting payment order shows VNPay method, status, countdown, pay button and cancel button', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  const checkout = db.checkout(SEED.users.an, 'test-detail-awaiting', {
    sku_ids: [SEED.skus.bowl11],
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
  const orderId = checkout.orders[0].id;
  renderApp(`/orders/${orderId}`, { as: 'an@example.com' });
  expect(await screen.findByText('VNPay')).toBeInTheDocument();
  expect(await screen.findByText('Chưa thanh toán')).toBeInTheDocument();
  expect(await screen.findByText(/Còn.*phút để thanh toán/)).toBeInTheDocument();
  expect(await screen.findByTestId('order-pay-button')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Huỷ đơn hàng' })).toBeInTheDocument();
});

test('buyer can cancel awaiting payment order, cancelling both orders of the checkout', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  db.setCartItem(SEED.users.an, SEED.skus.teeM, 1);
  const checkout = db.checkout(SEED.users.an, 'test-detail-cancel', {
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
  expect(checkout.orders).toHaveLength(2);
  const orderId = checkout.orders[0].id;
  const { user } = renderApp(`/orders/${orderId}`, { as: 'an@example.com' });
  await user.click(await screen.findByRole('button', { name: 'Huỷ đơn hàng' }));
  const dialog = await screen.findByRole('dialog', { name: 'Huỷ đơn hàng' });
  await user.type(within(dialog).getByLabelText('Lý do huỷ'), 'Không muốn thanh toán');
  await user.click(within(dialog).getByRole('button', { name: 'Xác nhận huỷ' }));
  expect(await screen.findByText('Đã huỷ', { selector: '[data-status]' })).toBeInTheDocument();
  const orders = getDb().state.orders.filter((o) => o.checkout_id === checkout.checkout_id);
  expect(orders).toHaveLength(2);
  expect(orders.every((o) => o.status === 'CANCELLED')).toBe(true);
});

test('seller sees commission info for online payment orders', async () => {
  renderApp(`/orders/${SEED.orders.paidCompleted}`, { as: 'binh@example.com' });
  const phiSanRow = await screen.findByText(/Phí sàn.*5%/);
  expect(within(phiSanRow.closest('div')!).getByText(/22\.500/)).toBeInTheDocument();
  const baoNhanRow = screen.getByText('Bạn nhận được');
  expect(within(baoNhanRow.closest('div')!).getByText(/457\.500/)).toBeInTheDocument();
});

test('seller does not see commission info for COD orders', async () => {
  renderApp(`/orders/${SEED.orders.delivered}`, { as: 'binh@example.com' });
  await screen.findByRole('heading', { level: 1, name: /Đơn hàng/ });
  expect(screen.queryByText(/Phí sàn/)).not.toBeInTheDocument();
  expect(screen.queryByText('Bạn nhận được')).not.toBeInTheDocument();
});

test('deadline passed error shows appropriate message', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  const checkout = db.checkout(SEED.users.an, 'test-deadline-passed', {
    sku_ids: [SEED.skus.bowl11],
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
  const orderId = checkout.orders[0].id;

  server.use(
    http.post('*/api/payments', () =>
      HttpResponse.json(
        {
          error: {
            code: 'INVALID_STATE_TRANSITION',
            message: 'deadline passed',
            details: { reason: 'DEADLINE_PASSED' },
          },
        },
        { status: 409 },
      ),
    ),
  );

  const { user } = renderApp(`/orders/${orderId}`, { as: 'an@example.com' });
  await user.click(await screen.findByTestId('order-pay-button'));
  expect(await screen.findByText('Đã quá hạn thanh toán, đơn hàng sẽ bị huỷ.')).toBeInTheDocument();
});

test('already paid order shows success message and refetch on pay click', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  const checkout = db.checkout(SEED.users.an, 'test-already-paid', {
    sku_ids: [SEED.skus.bowl11],
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
  const orderId = checkout.orders[0].id;

  const { user } = renderApp(`/orders/${orderId}`, { as: 'an@example.com' });
  await screen.findByRole('button', { name: 'Thanh toán' });

  // Simulate payment already created and succeeded
  const p = db.createPayment(SEED.users.an, 'pre', { checkout_id: checkout.checkout_id });
  db.submitMockPayment(p.id, 'succeed');

  // Click pay button again
  await user.click(await screen.findByRole('button', { name: 'Thanh toán' }));
  expect(await screen.findByText('Đơn hàng đã được thanh toán.')).toBeInTheDocument();
  expect(await screen.findByText('Đã thanh toán')).toBeInTheDocument();
  expect(vi.mocked(redirectTo)).not.toHaveBeenCalled();
});
