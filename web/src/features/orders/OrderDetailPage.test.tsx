import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { renderApp } from '@/test/render';

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
