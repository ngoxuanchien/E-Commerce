import { useEffect, useState } from 'react';
import { formatRemaining } from './payment-labels';

export function PaymentCountdown({ deadline }: { deadline: string }) {
  const [, setRefresh] = useState(0);

  useEffect(() => {
    const interval = setInterval(() => {
      setRefresh((r) => r + 1);
    }, 60_000);

    return () => clearInterval(interval);
  }, []);

  // eslint-disable-next-line react-hooks/purity
  const remaining = Date.parse(deadline) - Date.now();

  return (
    <p aria-live="polite" data-testid="payment-countdown">
      {formatRemaining(remaining)}
    </p>
  );
}
