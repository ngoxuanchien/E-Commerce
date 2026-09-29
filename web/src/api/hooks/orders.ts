import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import type { CheckoutRequest, CheckoutResponse } from '../types';

/**
 * Handlers are mutation options (not per-call), so they also run when the global
 * "Thử lại" toast re-executes the mutation with the same variables (same Idempotency-Key).
 */
export function useCheckout(handlers: { onSuccess?: (res: CheckoutResponse) => void; onError?: (err: unknown) => void } = {}) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ idempotencyKey, body }: { idempotencyKey: string; body: CheckoutRequest }) =>
      unwrap(api.POST('/api/checkout', { params: { header: { 'Idempotency-Key': idempotencyKey } }, body })),
    onSuccess: (res) => handlers.onSuccess?.(res),
    onError: (err) => handlers.onError?.(err),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: qk.cart });
      void queryClient.invalidateQueries({ queryKey: ['orders'] });
    },
  });
}
