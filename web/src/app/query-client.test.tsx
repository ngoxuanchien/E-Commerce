import { useQuery } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import { expect, test } from 'vitest';
import { api } from '@/api/client';
import { ERROR_MESSAGES, unwrap } from '@/api/errors';
import { server } from '@/test/server';
import { renderWithProviders } from '@/test/render';

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
