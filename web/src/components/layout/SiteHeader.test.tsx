import { act, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { qk } from '@/api/query-keys';
import { SEED } from '@/mocks/seed';
import { getDb } from '@/mocks/state';
import { currentPath, renderApp } from '@/test/render';

test('restores the session, shows the cart count and logs out', async () => {
  getDb().state.carts[SEED.users.an] = { [SEED.skus.bowl11]: 2, [SEED.skus.teeM]: 1 };
  const { user } = renderApp('/', { as: 'an@example.com' });
  expect(await screen.findByRole('link', { name: 'Giỏ hàng, 3 sản phẩm' })).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'An Nguyễn' }));
  await user.click(screen.getByRole('button', { name: 'Đăng xuất' }));

  expect(await screen.findByRole('link', { name: 'Đăng nhập' })).toBeInTheDocument();
  expect(getDb().state.session).toBeNull();
});

test('search submits to /search?q=', async () => {
  const { user } = renderApp('/');
  await user.type(await screen.findByRole('searchbox', { name: 'Tìm sản phẩm' }), 'bát tràng{Enter}');
  expect(currentPath()).toBe('/search?q=b%C3%A1t%20tr%C3%A0ng');
});

test('when refresh fails mid-session the user is sent to /login?next= once', async () => {
  const { queryClient } = renderApp('/?from=home', { as: 'an@example.com' });
  expect(await screen.findByRole('button', { name: 'An Nguyễn' })).toBeInTheDocument();
  const db = getDb();
  db.state.session = null; // refresh cookie expired
  db.state.tokens = {}; // access token expired
  await act(() => queryClient.invalidateQueries({ queryKey: qk.cart }));
  expect(await screen.findByRole('link', { name: 'Đăng nhập' })).toBeInTheDocument();
  expect(currentPath()).toBe('/login?next=%2F%3Ffrom%3Dhome');
});
