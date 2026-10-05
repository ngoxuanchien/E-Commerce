import { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { usePayment, useStartPayment } from '@/api/hooks/payments';
import { ApiError, paymentErrorMessage, paymentReasonOf } from '@/api/errors';
import { qk } from '@/api/query-keys';
import { EmptyState, ErrorState, PageSpinner } from '@/components/states';
import { Button } from '@/components/ui/button';
import { Link } from 'react-router';
import { toast } from 'sonner';
import { renewPayKey } from './pay-key';

export function PaymentResultPage() {
  const [searchParams] = useSearchParams();
  const paymentId = searchParams.get('payment_id') ?? '';
  const [timedOut, setTimedOut] = useState(false);
  const queryClient = useQueryClient();

  const { data: payment, error, isLoading, refetch } = usePayment(paymentId, !timedOut);
  const startPayment = useStartPayment();

  // Compute canClickButton early (before early returns, for hook ordering)
  const canClickButton = useMemo(() => {
    if (!payment) return false;
    const status = payment.status;
    const canRetry = status === 'FAILED' || status === 'EXPIRED';
    if (!canRetry) return false;
    const expiresAtTime = new Date(payment.expires_at).getTime();
    // eslint-disable-next-line react-hooks/purity
    const isExpired = expiresAtTime <= Date.now();
    return !isExpired;
  }, [payment]);

  // Set timeout after 60 seconds
  useEffect(() => {
    if (!paymentId) return;

    const timeout = setTimeout(() => {
      setTimedOut(true);
    }, 60_000);

    return () => clearTimeout(timeout);
  }, [paymentId]);

  // Invalidate orders when payment succeeds
  useEffect(() => {
    if (payment?.status === 'SUCCEEDED') {
      queryClient.invalidateQueries({ queryKey: ['orders'] });
      queryClient.invalidateQueries({ queryKey: ['order'] });
    }
  }, [payment?.status, queryClient]);

  // No payment ID provided
  if (!paymentId) {
    return (
      <EmptyState title="Không tìm thấy giao dịch">
        <Link to="/orders" className="font-medium text-tarp hover:underline">
          Xem đơn mua
        </Link>
      </EmptyState>
    );
  }

  // Loading state
  if (isLoading) {
    return <PageSpinner label="Đang tải…" />;
  }

  // Error state (NOT_FOUND vs other errors)
  if (error && !payment) {
    const isNotFound = error instanceof ApiError && error.code === 'NOT_FOUND';
    if (isNotFound) {
      return (
        <EmptyState title="Không tìm thấy giao dịch">
          <Link to="/orders" className="font-medium text-tarp hover:underline">
            Xem đơn mua
          </Link>
        </EmptyState>
      );
    }
    return <ErrorState error={error} onRetry={() => void refetch()} />;
  }

  // No data
  if (!payment) {
    return <PageSpinner label="Đang tải…" />;
  }

  const status = payment.status;

  const handlePayAgain = () => {
    startPayment.mutate(
      {
        checkoutId: payment.checkout_id,
        idempotencyKey: renewPayKey(payment.checkout_id),
      },
      {
        onError: (err) => {
          if (paymentReasonOf(err) === 'ALREADY_PAID') {
            queryClient.invalidateQueries({ queryKey: qk.payment(paymentId) });
            queryClient.invalidateQueries({ queryKey: ['orders'] });
            toast.success('Đơn hàng đã được thanh toán.');
          } else {
            toast.error(paymentErrorMessage(err));
          }
        },
      },
    );
  };

  return (
    <section data-testid="payment-result" data-status={status} aria-live="polite" className="rounded-lg border bg-card p-8 text-center">
      {status === 'SUCCEEDED' && (
        <>
          <h1 className="text-2xl font-bold">Thanh toán thành công</h1>
          <div className="mt-4">
            <Link to="/orders" className="font-medium text-tarp hover:underline">
              Xem đơn mua
            </Link>
          </div>
        </>
      )}

      {(status === 'FAILED' || status === 'EXPIRED') && (
        <>
          <h1 className="text-2xl font-bold">Thanh toán không thành công</h1>
          {canClickButton && (
            <Button onClick={handlePayAgain} disabled={startPayment.isPending} className="mt-4" data-testid="pay-again">
              Thanh toán lại
            </Button>
          )}
          <div className="mt-4">
            <Link to="/orders" className="font-medium text-tarp hover:underline">
              Xem đơn mua
            </Link>
          </div>
        </>
      )}

      {status === 'DUPLICATE' && (
        <>
          <h1 className="text-2xl font-bold">Giao dịch trùng — sẽ được hoàn tiền tự động</h1>
          <div className="mt-4">
            <Link to="/orders" className="font-medium text-tarp hover:underline">
              Xem đơn mua
            </Link>
          </div>
        </>
      )}

      {status === 'PENDING' && !timedOut && (
        <>
          <h1 className="text-2xl font-bold">Đang chờ kết quả thanh toán…</h1>
          <div className="mt-4">
            <PageSpinner label="Đang chờ kết quả thanh toán…" />
          </div>
        </>
      )}

      {status === 'PENDING' && timedOut && (
        <>
          <h1 className="text-2xl font-bold">Đang xác nhận thanh toán…</h1>
          <p className="mt-4 text-sm text-muted-foreground">Hệ thống sẽ cập nhật đơn hàng khi có kết quả.</p>
          <div className="mt-4">
            <Link to="/orders" className="font-medium text-tarp hover:underline">
              Xem đơn mua
            </Link>
          </div>
        </>
      )}
    </section>
  );
}
