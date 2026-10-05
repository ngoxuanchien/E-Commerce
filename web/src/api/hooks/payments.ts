import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import { redirectTo } from '@/lib/redirect';

/**
 * Start a payment for a checkout.
 * Silent mutation: callers handle errors themselves via onError per mutate call.
 */
export function useStartPayment() {
  return useMutation({
    mutationFn: ({ checkoutId, idempotencyKey }: { checkoutId: string; idempotencyKey: string }) =>
      unwrap(api.POST('/api/payments', { params: { header: { 'Idempotency-Key': idempotencyKey } }, body: { checkout_id: checkoutId } })),
    meta: { silent: true },
    onSuccess: (p) => redirectTo(p.redirect_url ?? `/checkout/result?payment_id=${p.id}`),
  });
}

/**
 * Poll for a payment by ID.
 * Polling interval depends on payment status: refetch every 2s while PENDING, stop on error or completion.
 */
export function usePayment(id: string, poll: boolean) {
  return useQuery({
    queryKey: qk.payment(id),
    queryFn: () => unwrap(api.GET('/api/payments/{id}', { params: { path: { id } } })),
    enabled: id !== '',
    staleTime: 0,
    refetchInterval: (q) => {
      const shouldPoll = poll && q.state.status !== 'error' && (q.state.data?.status ?? 'PENDING') === 'PENDING';
      return shouldPoll ? 2000 : false;
    },
  });
}
