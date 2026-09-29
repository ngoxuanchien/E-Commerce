import { QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { expect, test } from 'vitest';
import { createQueryClient } from '@/app/query-client';
import { useProductSearch } from './catalog';

test('useProductSearch follows next_cursor until the last page without duplicates', async () => {
  const client = createQueryClient({ retry: false });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result } = renderHook(() => useProductSearch({ sort: 'price_asc' }, 3), { wrapper });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  while (result.current.hasNextPage) {
    await act(() => result.current.fetchNextPage());
  }
  const ids = result.current.data!.pages.flatMap((p) => p.items.map((i) => i.id));
  expect(ids).toHaveLength(7);
  expect(new Set(ids).size).toBe(7);
  const prices = result.current.data!.pages.flatMap((p) => p.items.map((i) => i.min_price));
  expect(prices).toEqual([...prices].sort((a, b) => a - b));
});
