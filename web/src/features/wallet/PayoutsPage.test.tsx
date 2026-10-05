import { screen, waitFor, act } from '@testing-library/react';
import { expect, test, beforeEach, afterEach, vi } from 'vitest';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';
import { formatVnd } from '@/lib/format';
import { PAYOUT_STATUS_LABEL } from './wallet-labels';

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

test('after createPayout, render /seller/payouts → one payout-row with data-status="REQUESTED" and label', async () => {
  getDb().createPayout(SEED.users.binh, 'k', { amount: 100_000 });
  renderApp('/seller/payouts', { as: 'binh@example.com' });

  const row = await screen.findByTestId('payout-row');
  expect(row).toHaveAttribute('data-status', 'REQUESTED');
  expect(row.textContent).toContain(PAYOUT_STATUS_LABEL.REQUESTED);
  expect(row.textContent).toContain(formatVnd(100_000));
});

test('with fake timers, advance 10 seconds → row status becomes SETTLED', async () => {
  getDb().createPayout(SEED.users.binh, 'k', { amount: 100_000 });
  renderApp('/seller/payouts', { as: 'binh@example.com' });

  const row = await screen.findByTestId('payout-row');
  expect(row).toHaveAttribute('data-status', 'REQUESTED');

  // Advance time by 10 seconds to trigger polling and settle
  await act(() => vi.advanceTimersByTimeAsync(10_000));

  await waitFor(() => {
    expect(row).toHaveAttribute('data-status', 'SETTLED');
  });

  expect(row.textContent).toContain(PAYOUT_STATUS_LABEL.SETTLED);
});

test('clicking "Đã chuyển" tab → currentPath ends with ?status=SETTLED', async () => {
  getDb().createPayout(SEED.users.binh, 'k', { amount: 100_000 });
  const { user } = renderApp('/seller/payouts', { as: 'binh@example.com' });

  const settledTab = await screen.findByRole('button', { name: PAYOUT_STATUS_LABEL.SETTLED });
  await user.click(settledTab);

  expect(currentPath()).toContain('?status=SETTLED');
});

test('PayoutsPage shows back link to /seller/wallet', async () => {
  renderApp('/seller/payouts', { as: 'binh@example.com' });

  const backLink = await screen.findByRole('link', { name: 'Về ví' });
  expect(backLink).toHaveAttribute('href', '/seller/wallet');
});

test('no payouts → empty state "Chưa có yêu cầu rút tiền nào."', async () => {
  renderApp('/seller/payouts', { as: 'binh@example.com' });

  expect(await screen.findByText('Chưa có yêu cầu rút tiền nào.')).toBeInTheDocument();
});
