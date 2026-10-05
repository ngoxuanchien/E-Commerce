import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { renderApp } from '@/test/render';

const card = () => screen.findByRole('article', { name: 'Đơn hàng của Áo Thun Sài Gòn' });

test('confirm → ship → deliver walks the order through the state machine', async () => {
  const { user } = renderApp('/seller/orders', { as: 'chi@example.com' });
  const order = await card();
  expect(within(order).getByText('Người nhận: An Nguyễn, 0901234567')).toBeInTheDocument();
  await user.click(within(order).getByRole('button', { name: 'Xác nhận đơn' }));
  await user.click(await within(await card()).findByRole('button', { name: 'Bắt đầu giao' }));
  await user.click(await within(await card()).findByRole('button', { name: 'Xác nhận đã giao' }));
  expect(await within(await card()).findByText('Đã giao', { selector: '[data-status]' })).toBeInTheDocument();
  expect(getDb().order(SEED.users.chi, SEED.orders.pending).status).toBe('DELIVERED');
});

test('the seller cancels with a reason', async () => {
  const { user } = renderApp('/seller/orders', { as: 'chi@example.com' });
  await user.click(within(await card()).getByRole('button', { name: 'Huỷ đơn' }));
  const dialog = await screen.findByRole('dialog', { name: 'Huỷ đơn hàng' });
  await user.type(within(dialog).getByLabelText('Lý do huỷ'), 'Hết vải');
  await user.click(within(dialog).getByRole('button', { name: 'Xác nhận huỷ' }));
  expect(await within(await card()).findByText('Đã huỷ', { selector: '[data-status]' })).toBeInTheDocument();
});

test('a stale action reports the state change and refreshes the list', async () => {
  const { user } = renderApp('/seller/orders', { as: 'chi@example.com' });
  const order = await card();
  getDb().buyerAction(SEED.users.an, SEED.orders.pending, 'cancel', 'Đổi ý'); // the buyer cancels first
  await user.click(within(order).getByRole('button', { name: 'Xác nhận đơn' }));
  expect(await screen.findByText('Trạng thái đơn hàng đã thay đổi. Vui lòng tải lại trang.')).toBeInTheDocument();
  expect(await within(await card()).findByText('Đã huỷ', { selector: '[data-status]' })).toBeInTheDocument();
});

test('status tabs filter the seller orders', async () => {
  const { user } = renderApp('/seller/orders', { as: 'binh@example.com' });
  const articles = await screen.findAllByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' });
  expect(articles.length).toBeGreaterThan(0);
  await user.click(screen.getByRole('button', { name: 'Chờ xác nhận' }));
  expect(await screen.findByText('Chưa có đơn hàng nào ở trạng thái này.')).toBeInTheDocument();
});

test('shows waiting for buyer to pay message for awaiting payment orders', async () => {
  const db = getDb();
  db.setCartItem(SEED.users.an, SEED.skus.bowl11, 1);
  db.checkout(SEED.users.an, 'test-seller-awaiting', {
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
  renderApp('/seller/orders', { as: 'binh@example.com' });
  const articles = await screen.findAllByRole('article', { name: 'Đơn hàng của Gốm Bát Tràng Cô Bình' });
  const gom = articles.find((a) => within(a).queryByText('Chờ người mua thanh toán') !== null);
  expect(gom).toBeDefined();
  expect(within(gom!).queryByRole('button', { name: 'Xác nhận đơn' })).not.toBeInTheDocument();
});
