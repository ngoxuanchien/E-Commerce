import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../client';
import { unwrap } from '../errors';
import { qk } from '../query-keys';
import type { PayoutStatus } from '../types';

export function useWallet() {
  return useQuery({
    queryKey: qk.wallet,
    queryFn: () => unwrap(api.GET('/api/seller/wallet')),
  });
}

export function useWalletEntries() {
  return useInfiniteQuery({
    queryKey: qk.walletEntries,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/seller/wallet/entries', { params: { query: { cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function usePayouts(status?: PayoutStatus) {
  return useInfiniteQuery({
    queryKey: qk.payouts(status),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/api/seller/payouts', { params: { query: { status, cursor: pageParam } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: (q) => q.state.data?.pages.some((p) => p.items.some((x) => x.status === 'REQUESTED')) ? 10_000 : false,
  });
}

export function useCreatePayout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ idempotencyKey, amount }: { idempotencyKey: string; amount: number }) =>
      unwrap(api.POST('/api/seller/payouts', { params: { header: { 'Idempotency-Key': idempotencyKey } }, body: { amount } })),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: qk.wallet });
      void queryClient.invalidateQueries({ queryKey: qk.walletEntries });
      void queryClient.invalidateQueries({ queryKey: ['payouts'] });
    },
  });
}
