import { screen, waitFor } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import { expect, test } from 'vitest';
import { server } from '@/test/server';
import { renderApp } from '@/test/render';
import { formatVnd } from '@/lib/format';

test('typing 100000 and submitting → toast and key sent', async () => {
  const requests: string[] = [];
  server.events.on('request:start', ({ request }) => {
    if (request.url.includes('/api/seller/payouts')) {
      requests.push(request.headers.get('Idempotency-Key') || '');
    }
  });

  const { user } = renderApp('/seller/wallet', { as: 'binh@example.com' });

  const input = await screen.findByTestId('payout-amount');
  await user.type(input, '100000');

  const submitBtn = screen.getByTestId('payout-submit');
  await user.click(submitBtn);

  expect(await screen.findByText('Đã gửi yêu cầu rút tiền')).toBeInTheDocument();
  expect(requests.length).toBe(1);
  expect(requests[0]).toBeTruthy();

  const pendingPayoutCard = screen.getByTestId('wallet-pending-payout');
  await waitFor(() => {
    const content = pendingPayoutCard.textContent || '';
    expect(content).toContain(formatVnd(100_000));
  });
});

test('second payout sends a different key', async () => {
  const requests: string[] = [];
  server.events.on('request:start', ({ request }) => {
    if (request.url.includes('/api/seller/payouts')) {
      requests.push(request.headers.get('Idempotency-Key') || '');
    }
  });

  const { user } = renderApp('/seller/wallet', { as: 'binh@example.com' });

  const input = await screen.findByTestId('payout-amount');
  await user.type(input, '100000');

  const submitBtn = screen.getByTestId('payout-submit');
  await user.click(submitBtn);

  // Wait for first request
  await waitFor(() => {
    expect(requests.length).toBe(1);
  });

  // Wait for form reset
  const input2 = screen.getByTestId('payout-amount') as HTMLInputElement;
  await waitFor(() => {
    expect(input2.value).toBe('');
  });

  // Submit second payout
  await user.type(input2, '60000');
  await user.click(submitBtn);

  // Wait for second request
  await waitFor(() => {
    expect(requests.length).toBe(2);
  });

  expect(requests[0]).not.toBe(requests[1]);
});

test('typing 40000 → min message, no POST sent', async () => {
  let postAttempted = false;
  server.events.on('request:start', ({ request }) => {
    if (request.url.includes('/api/seller/payouts') && request.method === 'POST') {
      postAttempted = true;
    }
  });

  const { user } = renderApp('/seller/wallet', { as: 'binh@example.com' });

  const input = await screen.findByTestId('payout-amount');
  await user.type(input, '40000');

  // Blur to trigger validation
  await user.click(screen.getByTestId('payout-submit'));

  // Check for error message in the form
  await screen.findByText('Số tiền rút tối thiểu là 50.000 ₫');
  expect(postAttempted).toBe(false);
});

test('INSUFFICIENT_FUNDS error → alert with available balance', async () => {
  server.use(
    http.post('*/api/seller/payouts', () =>
      HttpResponse.json(
        { error: { code: 'INSUFFICIENT_FUNDS', message: 'x', details: { available: 12_000 } } },
        { status: 409 },
      ),
    ),
  );

  const { user } = renderApp('/seller/wallet', { as: 'binh@example.com' });

  const input = await screen.findByTestId('payout-amount');
  await user.type(input, '50000');

  const submitBtn = screen.getByTestId('payout-submit');
  await user.click(submitBtn);

  // Wait for the error message to appear (the api call and error handler should run)
  const alert = await screen.findByRole('alert');
  const expectedMessage = `Số dư không đủ. Số dư khả dụng: ${formatVnd(12_000)}.`;
  expect(alert.textContent?.trim()).toBe(expectedMessage);
});
