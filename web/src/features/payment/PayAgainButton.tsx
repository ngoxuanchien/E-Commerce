import { toast } from 'sonner';
import type { Order } from '@/api/types';
import { useStartPayment } from '@/api/hooks/payments';
import { paymentErrorMessage, paymentReasonOf } from '@/api/errors';
import { useQueryClient } from '@tanstack/react-query';
import { qk } from '@/api/query-keys';
import { Button } from '@/components/ui/button';
import { renewPayKey } from './pay-key';

export function PayAgainButton({ order, size = 'default' }: { order: Order; size?: 'sm' | 'default' }) {
  const startPayment = useStartPayment();
  const queryClient = useQueryClient();

  const handleClick = () => {
    startPayment.mutate(
      { checkoutId: order.checkout_id, idempotencyKey: renewPayKey(order.checkout_id) },
      {
        onError: (err) => {
          const reason = paymentReasonOf(err);
          if (reason === 'ALREADY_PAID') {
            void queryClient.invalidateQueries({ queryKey: ['orders'] });
            void queryClient.invalidateQueries({ queryKey: qk.order(order.id) });
            toast.success('Đơn hàng đã được thanh toán.');
          } else {
            toast.error(paymentErrorMessage(err));
            if ((err as any).code === 'INVALID_STATE_TRANSITION') {
              void queryClient.invalidateQueries({ queryKey: ['orders'] });
              void queryClient.invalidateQueries({ queryKey: qk.order(order.id) });
            }
          }
        },
      },
    );
  };

  return (
    <Button
      data-testid="order-pay-button"
      size={size}
      disabled={startPayment.isPending}
      onClick={handleClick}
    >
      Thanh toán
    </Button>
  );
}
