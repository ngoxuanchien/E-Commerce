import { act, screen } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { PaymentCountdown } from './PaymentCountdown';
import { renderWithProviders } from '@/test/render';

test('PaymentCountdown renders initial deadline and updates', async () => {
  vi.useFakeTimers();
  try {
    const now = Date.now();
    const deadline = new Date(now + 2 * 60_000 + 30_000).toISOString();

    renderWithProviders(<PaymentCountdown deadline={deadline} />);

    expect(screen.getByTestId('payment-countdown')).toHaveTextContent('Còn 2 phút để thanh toán');
    expect(screen.getByTestId('payment-countdown')).toHaveAttribute('aria-live', 'polite');

    act(() => {
      vi.advanceTimersByTime(60_000);
    });

    expect(screen.getByTestId('payment-countdown')).toHaveTextContent('Còn 1 phút để thanh toán');
  } finally {
    vi.useRealTimers();
  }
});
