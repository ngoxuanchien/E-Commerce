import { useMutation, useQuery } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import { afterEach, expect, test } from 'vitest';
import { toast } from 'sonner';
import { api } from '@/api/client';
import { ApiError, ERROR_MESSAGES, unwrap } from '@/api/errors';
import { server } from '@/test/server';
import { renderWithProviders } from '@/test/render';

// Dismiss any lingering toasts between tests
afterEach(() => toast.dismiss());

function Probe() {
  const q = useQuery({ queryKey: ['categories'], queryFn: () => unwrap(api.GET('/api/categories')) });
  return <p>{q.isSuccess ? 'đã tải' : 'đang chờ'}</p>;
}

test('UPSTREAM_UNAVAILABLE shows a toast whose retry action refetches', async () => {
  let calls = 0;
  server.use(
    http.get('*/api/categories', () => {
      calls += 1;
      return calls === 1
        ? HttpResponse.json({ error: { code: 'UPSTREAM_UNAVAILABLE', message: 'down' } }, { status: 503 })
        : HttpResponse.json([]);
    }),
  );
  const { user } = renderWithProviders(<Probe />);
  expect(await screen.findByText(ERROR_MESSAGES.UPSTREAM_UNAVAILABLE)).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Thử lại' }));
  expect(await screen.findByText('đã tải')).toBeInTheDocument();
});

test('mutation with meta.silent=true does not show error toast', async () => {
  const errors: unknown[] = [];
  function SilentMut() {
    const mut = useMutation({
      mutationFn: () => Promise.reject(new ApiError(503, 'UPSTREAM_UNAVAILABLE', 'x')),
      meta: { silent: true },
      onError: (err) => errors.push(err), // Caller handles error
    });
    return <button onClick={() => mut.mutate()}>Go</button>;
  }

  const { user } = renderWithProviders(<SilentMut />);
  await user.click(screen.getByRole('button', { name: 'Go' }));
  // Let async handlers settle
  await new Promise((r) => setTimeout(r, 200));
  // Mutation fails, but silent:true skips the error toast
  expect(errors.length).toBe(1);
  // The toast should NOT be visible because meta.silent=true
  expect(screen.queryByText(ERROR_MESSAGES.UPSTREAM_UNAVAILABLE)).not.toBeInTheDocument();
});

test('mutation without meta.silent shows error toast with retry button', async () => {
  function NormalMut() {
    const mut = useMutation({
      mutationFn: () => Promise.reject(new ApiError(503, 'UPSTREAM_UNAVAILABLE', 'x')),
    });
    return <button onClick={() => mut.mutate()}>Go</button>;
  }

  const { user } = renderWithProviders(<NormalMut />);
  await user.click(screen.getByRole('button', { name: 'Go' }));
  // Without meta.silent, the error toast is shown with a retry button
  expect(await screen.findByText(ERROR_MESSAGES.UPSTREAM_UNAVAILABLE)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Thử lại' })).toBeInTheDocument();
});
