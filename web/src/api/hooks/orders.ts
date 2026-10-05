import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../client';
import { ApiError, isRetryable, unwrap } from '../errors';
import { qk } from '../query-keys';
import type { CheckoutRequest, CheckoutResponse, Order, OrderStatus } from '../types';

/**
 * Handlers are mutation options (not per-call), so they also run when the global
 * "Thử lại" toast re-executes the mutation with the same variables (same Idempotency-Key).
 */
export function useCheckout(handlers: { onSuccess?: (res: CheckoutResponse, vars: { body: CheckoutRequest }) => void; onError?: (err: unknown) => void } = {}) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ idempotencyKey, body }: { idempotencyKey: string; body: CheckoutRequest }) =>
      unwrap(api.POST('/api/checkout', { params: { header: { 'Idempotency-Key': idempotencyKey } }, body })),
    onSuccess: (res, vars) => handlers.onSuccess?.(res, vars),
    onError: (err) => handlers.onError?.(err),
    onSettled: (_res, err) => {
      // After a retryable failure (network, 503, 429) the server may already have
      // placed the orders and emptied the cart. Refetching it would replace the
      // checkout form with an empty state; keep the form so the buyer can retry
      // with the same Idempotency-Key, and refresh the cart once the retry settles.
      if (!isRetryable(err)) void queryClient.invalidateQueries({ queryKey: qk.cart });
      void queryClient.invalidateQueries({ queryKey: ['orders'] });
    },
  });
}

export function useMyOrders(status?: OrderStatus) {
  return useInfiniteQuery({
    queryKey: qk.orders(status),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/orders', { params: { query: { status, cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useOrder(id: string) {
  return useQuery({ queryKey: qk.order(id), queryFn: () => unwrap(api.GET('/api/orders/{id}', { params: { path: { id } } })) });
}

/** After a transition store the returned order and refresh every order list. */
export function useOrderCacheUpdate() {
  const queryClient = useQueryClient();
  const refreshLists = () => {
    void queryClient.invalidateQueries({ queryKey: ['orders'] });
    void queryClient.invalidateQueries({ queryKey: ['seller-orders'] });
  };
  return {
    onSuccess: (order: Order) => {
      queryClient.setQueryData(qk.order(order.id), order);
      refreshLists();
    },
    onError: (err: unknown, variables: { id: string } | string) => {
      // Someone else moved the order first: show its real state.
      if (err instanceof ApiError && err.code === 'INVALID_STATE_TRANSITION') {
        void queryClient.invalidateQueries({ queryKey: qk.order(typeof variables === 'string' ? variables : variables.id) });
        refreshLists();
      }
    },
  };
}

export function useCancelMyOrder() {
  const cache = useOrderCacheUpdate();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) =>
      unwrap(api.POST('/api/orders/{id}/cancel', { params: { path: { id } }, body: { reason } })),
    ...cache,
  });
}

export function useCompleteMyOrder() {
  const cache = useOrderCacheUpdate();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.POST('/api/orders/{id}/complete', { params: { path: { id } } })),
    ...cache,
  });
}

export function useSellerOrders(status?: OrderStatus) {
  return useInfiniteQuery({
    queryKey: qk.sellerOrders(status),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/seller/orders', { params: { query: { status, cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useSellerOrderAction() {
  const cache = useOrderCacheUpdate();
  return useMutation({
    mutationFn: ({ id, action, reason }: { id: string; action: 'confirm' | 'ship' | 'deliver' | 'cancel'; reason?: string }) => {
      const path = { params: { path: { id } } };
      switch (action) {
        case 'confirm':
          return unwrap(api.POST('/api/seller/orders/{id}/confirm', path));
        case 'ship':
          return unwrap(api.POST('/api/seller/orders/{id}/ship', path));
        case 'deliver':
          return unwrap(api.POST('/api/seller/orders/{id}/deliver', path));
        case 'cancel':
          return unwrap(api.POST('/api/seller/orders/{id}/cancel', { ...path, body: { reason: reason ?? '' } }));
      }
    },
    ...cache,
  });
}
