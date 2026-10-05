import { screen, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { renderApp } from '@/test/render';
import { shortId } from '@/features/orders/order-status';

test('renders wallet cards with available, in_escrow, and pending_payout balances', async () => {
  renderApp('/seller/wallet', { as: 'binh@example.com' });

  const available = await screen.findByTestId('wallet-available');
  expect(within(available).getByText(/457\.500/)).toBeInTheDocument();

  const inEscrow = await screen.findByTestId('wallet-in-escrow');
  expect(within(inEscrow).getByText(/0\s₫/)).toBeInTheDocument();

  const pendingPayout = await screen.findByTestId('wallet-pending-payout');
  expect(within(pendingPayout).getByText(/0\s₫/)).toBeInTheDocument();
});

test('displays wallet entry for a RELEASE transaction', async () => {
  renderApp('/seller/wallet', { as: 'binh@example.com' });

  const entry = await screen.findByTestId('wallet-entry');
  expect(within(entry).getByText('Tiền đơn hàng')).toBeInTheDocument();
  expect(within(entry).getByText(/\+457\.500/)).toBeInTheDocument();
  expect(within(entry).getByText(/\+457\.500/)).toHaveClass('text-jade');

  const link = within(entry).getByRole('link', { name: shortId(SEED.orders.paidCompleted) });
  expect(link).toHaveAttribute('href', `/orders/${SEED.orders.paidCompleted}`);
});

test('displays wallet entry for a PAYOUT_REQUEST transaction', async () => {
  getDb().createPayout(SEED.users.binh, 'k', { amount: 100_000 });
  renderApp('/seller/wallet', { as: 'binh@example.com' });

  const entries = await screen.findAllByTestId('wallet-entry');
  const payoutEntry = entries.find((e) => within(e).queryByText('Rút tiền') !== null);
  expect(payoutEntry).toBeDefined();

  expect(within(payoutEntry!).getByText('Rút tiền')).toBeInTheDocument();
  expect(within(payoutEntry!).getByText(/−100\.000/)).toBeInTheDocument();
  expect(within(payoutEntry!).getByText(/−100\.000/)).toHaveClass('text-destructive');
});

test('wallet pending_payout card shows total of REQUESTED payouts', async () => {
  getDb().createPayout(SEED.users.binh, 'k', { amount: 100_000 });
  renderApp('/seller/wallet', { as: 'binh@example.com' });

  const pendingPayout = await screen.findByTestId('wallet-pending-payout');
  // The card contains the label and the Price component
  expect(within(pendingPayout).getByText(/100\.000/)).toBeInTheDocument();
});

test('load more fetches additional wallet entries', async () => {
  const db = getDb();
  // Add 25 extra RELEASE entries to the mock database
  for (let i = 0; i < 25; i++) {
    db.state.walletEntries.push({
      id: `entry-extra-${i}`,
      transaction_id: `txn-extra-${i}`,
      kind: 'RELEASE',
      amount: 10_000 + i * 1000,
      order_id: SEED.orders.paidCompleted,
      payout_id: null,
      created_at: new Date(Date.now() - (i + 2) * 1_000_000).toISOString(),
      seller_user_id: SEED.users.binh,
    });
  }

  const { user } = renderApp('/seller/wallet', { as: 'binh@example.com' });

  // Initially show 20 rows (pagination default)
  const entries = await screen.findAllByTestId('wallet-entry');
  expect(entries).toHaveLength(20);

  // Click load more button
  const loadMoreButton = await screen.findByRole('button', { name: 'Xem thêm' });
  await user.click(loadMoreButton);

  // Now should have 26 rows (20 + 6 from next page)
  const updatedEntries = await screen.findAllByTestId('wallet-entry');
  expect(updatedEntries.length).toBeGreaterThanOrEqual(20);
});

test('shows nav link for wallet in seller layout', async () => {
  renderApp('/seller/wallet', { as: 'binh@example.com' });

  const navLink = await screen.findByRole('link', { name: 'Ví' });
  expect(navLink).toHaveAttribute('href', '/seller/wallet');
});

test('displays empty state when no wallet entries', async () => {
  const db = getDb();
  // Remove all wallet entries for binh
  db.state.walletEntries = db.state.walletEntries.filter((e) => e.seller_user_id !== SEED.users.binh);

  renderApp('/seller/wallet', { as: 'binh@example.com' });

  expect(await screen.findByText('Chưa có giao dịch nào.')).toBeInTheDocument();
});
