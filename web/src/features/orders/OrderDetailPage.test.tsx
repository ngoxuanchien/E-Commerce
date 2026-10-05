import { screen, within } from '@testing-library/react';
import { expect, test, vi, beforeEach } from 'vitest';
import { SEED } from '@/mocks/seed';
import { formatVnd } from '@/lib/format';
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

test('awaiting payment order shows VNPay method, status, countdown and pay button', async () => {
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
});

test('buyer can cancel awaiting payment order', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  const checkout = db.checkout(SEED.users.an, 'test-detail-cancel', {
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
  await user.click(await screen.findByRole('button', { name: 'Huỷ đơn hàng' }));
  const dialog = await screen.findByRole('dialog', { name: 'Huỷ đơn hàng' });
  await user.type(within(dialog).getByLabelText('Lý do huỷ'), 'Không muốn thanh toán');
  await user.click(within(dialog).getByRole('button', { name: 'Xác nhận huỷ' }));
  expect(await screen.findByText('Đã huỷ', { selector: '[data-status]' })).toBeInTheDocument();
  const orders = getDb().state.orders.filter((o) => o.checkout_id === checkout.checkout_id);
  expect(orders).toHaveLength(1);
  expect(orders[0].status).toBe('CANCELLED');
});

test('seller sees commission info for online payment orders', async () => {
  renderApp(`/orders/${SEED.orders.paidCompleted}`, { as: 'binh@example.com' });
  expect(await screen.findByText(/Phí sàn.*5%/)).toBeInTheDocument();
  await screen.findByText(/22\.500/); // commission amount in vi-VN format
  expect(screen.getByText('Bạn nhận được')).toBeInTheDocument();
  await screen.findByText(/457\.500/); // seller receives in vi-VN format
});

test('seller does not see commission info for COD orders', async () => {
  renderApp(`/orders/${SEED.orders.delivered}`, { as: 'binh@example.com' });
  expect(screen.queryByText(/Phí sàn/)).not.toBeInTheDocument();
  expect(screen.queryByText('Bạn nhận được')).not.toBeInTheDocument();
});
