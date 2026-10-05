import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { errorMessage, isRetryable } from '@/api/errors';

const RETRY_LABEL = 'Thử lại';

/** RATE_LIMITED / UPSTREAM_UNAVAILABLE surface as a toast with a retry action; other errors are handled where they occur. */
export function createQueryClient(options: { retry?: boolean } = {}): QueryClient {
  const allowRetry = options.retry ?? true;
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error, query) => {
        if (!isRetryable(error)) return;
        toast.error(errorMessage(error), { id: `query-${query.queryHash}`, action: { label: RETRY_LABEL, onClick: () => void query.fetch() } });
      },
    }),
    mutationCache: new MutationCache({
      onError: (error, variables, _onMutateResult, mutation) => {
        // Silent mutations handle their own errors; skip the global toast.
        if (mutation.meta?.silent === true) return;
        if (!isRetryable(error)) return;
        toast.error(errorMessage(error), { action: { label: RETRY_LABEL, onClick: () => void mutation.execute(variables) } });
      },
    }),
    defaultOptions: {
      queries: { retry: (count, error) => allowRetry && isRetryable(error) && count < 1, staleTime: 30_000, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}
